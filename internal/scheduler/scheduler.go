package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/service"
)

type Sender interface {
	SendChatMessage(ctx context.Context, telegramID int64, text string) error
}

type Scheduler struct {
	service      *service.Service
	sender       Sender
	digestHour   int
	digestMinute int
	metrics      *metrics.Registry
	logger       *logging.Logger
	now          func() time.Time
}

type Option func(*Scheduler)

func WithMetrics(registry *metrics.Registry) Option {
	return func(s *Scheduler) {
		s.metrics = registry
	}
}

func WithLogger(logger *logging.Logger) Option {
	return func(s *Scheduler) {
		s.logger = logger
	}
}

func New(service *service.Service, sender Sender, digestHour int, digestMinute int, opts ...Option) *Scheduler {
	scheduler := &Scheduler{
		service:      service,
		sender:       sender,
		digestHour:   digestHour,
		digestMinute: digestMinute,
		now:          time.Now,
	}
	for _, opt := range opts {
		opt(scheduler)
	}
	return scheduler
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	start := time.Now()
	defer func() {
		if s.metrics != nil {
			s.metrics.ObserveDuration("scheduler_iteration_duration_seconds", nil, start)
		}
	}()
	now := s.now()
	if err := s.sendReminders(ctx, now); err != nil {
		if s.logger != nil {
			s.logger.Error("scheduler_reminders_failed", err, nil)
		} else {
			log.Printf("scheduler reminders error: %v", err)
		}
	}
	if err := s.sendDigests(ctx, now); err != nil {
		if s.logger != nil {
			s.logger.Error("scheduler_digests_failed", err, nil)
		} else {
			log.Printf("scheduler digests error: %v", err)
		}
	}
}

func (s *Scheduler) sendReminders(ctx context.Context, now time.Time) error {
	notifications, err := s.service.DueReminders(ctx, now, 100)
	if err != nil {
		return err
	}
	for _, notification := range notifications {
		if err := s.sender.SendChatMessage(ctx, notification.User.TelegramID, formatReminder(notification.Task, locationForUser(notification.User))); err != nil {
			return err
		}
		if err := s.service.MarkReminderSent(ctx, notification.Reminder.ID, now); err != nil {
			return err
		}
		if _, err := s.service.ScheduleNextRecurringReminder(ctx, notification, now); err != nil {
			return err
		}
		if s.metrics != nil {
			s.metrics.Inc("reminder_sent_total", metrics.Labels{
				"workspace_id": fmt.Sprint(notification.Task.WorkspaceID),
				"user_id":      fmt.Sprint(notification.User.ID),
			})
		}
		if s.logger != nil {
			s.logger.Info("reminder_sent", logging.Fields{
				"user_id":      notification.User.ID,
				"workspace_id": notification.Task.WorkspaceID,
				"task_id":      notification.Task.ID,
				"reminder_id":  notification.Reminder.ID,
			})
		}
	}
	return nil
}

func (s *Scheduler) sendDigests(ctx context.Context, now time.Time) error {
	digests, err := s.service.DueDigests(ctx, now, s.digestHour, s.digestMinute)
	if err != nil {
		return err
	}
	for _, digest := range digests {
		if err := s.sender.SendChatMessage(ctx, digest.User.TelegramID, formatDigest(digest)); err != nil {
			return err
		}
		if err := s.service.MarkDigestSent(ctx, digest.User.ID, digest.DigestDate, now); err != nil {
			return err
		}
		if s.metrics != nil {
			s.metrics.Inc("digest_sent_total", metrics.Labels{"user_id": fmt.Sprint(digest.User.ID)})
		}
		if s.logger != nil {
			s.logger.Info("digest_sent", logging.Fields{
				"user_id":     digest.User.ID,
				"digest_date": digest.DigestDate.Format("2006-01-02"),
				"task_count":  len(digest.Tasks),
			})
		}
	}
	return nil
}

func formatReminder(task domain.Task, location *time.Location) string {
	text := "🔔 Напоминание\n\n" + task.Title
	if task.DueAt != nil {
		text += "\nСрок задачи: " + task.DueAt.In(location).Format("02.01.2006 15:04")
	}
	return text
}

func formatDigest(digest service.DigestNotification) string {
	if len(digest.Tasks) == 0 {
		return "Доброе утро! На сегодня задач нет."
	}
	groups := map[domain.Priority][]domain.Task{
		domain.PriorityP1: {},
		domain.PriorityP2: {},
		domain.PriorityP3: {},
		domain.PriorityP4: {},
	}
	for _, task := range digest.Tasks {
		groups[task.Priority] = append(groups[task.Priority], task)
	}

	var builder strings.Builder
	builder.WriteString("Доброе утро! Задачи на сегодня:")
	for _, priority := range []domain.Priority{domain.PriorityP1, domain.PriorityP2, domain.PriorityP3, domain.PriorityP4} {
		tasks := groups[priority]
		if len(tasks) == 0 {
			continue
		}
		builder.WriteString("\n\n")
		builder.WriteString(strings.ToUpper(string(priority)))
		for _, task := range tasks {
			builder.WriteString(fmt.Sprintf("\n- %s", task.Title))
			if task.DueAt != nil {
				builder.WriteString(" — ")
				builder.WriteString(task.DueAt.In(locationForUser(digest.User)).Format("15:04"))
			}
		}
	}
	return builder.String()
}

func locationForUser(user domain.User) *time.Location {
	if user.Timezone != "" {
		if location, err := time.LoadLocation(user.Timezone); err == nil {
			return location
		}
	}
	return time.Local
}
