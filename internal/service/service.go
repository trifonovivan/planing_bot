package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/parser"
)

var ErrUnknownPostponeOption = errors.New("unknown postpone option")

type Store interface {
	EnsureUser(ctx context.Context, user domain.TelegramUser, defaultTimezone string) (*domain.User, error)
	EnsurePersonalWorkspace(ctx context.Context, userID int64) (*domain.Workspace, error)
	CreateTask(ctx context.Context, task *domain.Task) error
	CreateTaskReminder(ctx context.Context, taskID int64, remindAt time.Time) error
	CreateTaskEvent(ctx context.Context, taskID int64, userID int64, eventType string, payload any) error
	TaskByID(ctx context.Context, taskID int64) (*domain.Task, error)
	TaskRecipient(ctx context.Context, taskID int64) (*domain.User, error)
	UpdateTaskStatus(ctx context.Context, taskID int64, userID int64, status domain.Status, at time.Time) (*domain.Task, error)
	PostponeTask(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error)
	TasksForRange(ctx context.Context, userID int64, start time.Time, end time.Time) ([]domain.Task, error)
	DueReminderNotifications(ctx context.Context, now time.Time, limit int) ([]ReminderNotification, error)
	MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error
	UsersForDigest(ctx context.Context) ([]domain.User, error)
	HasDigestRun(ctx context.Context, userID int64, digestDate time.Time) (bool, error)
	MarkDigestRun(ctx context.Context, userID int64, digestDate time.Time, sentAt time.Time) error
}

type Service struct {
	store           Store
	defaultTimezone string
	defaultLocation *time.Location
	metrics         *metrics.Registry
	logger          *logging.Logger
	now             func() time.Time
}

type Option func(*Service)

type CreateTaskResult struct {
	Task  domain.Task
	Parse parser.ParseResult
}

type ReminderNotification struct {
	Reminder domain.TaskReminder
	Task     domain.Task
	User     domain.User
}

type DigestNotification struct {
	User       domain.User
	DigestDate time.Time
	Tasks      []domain.Task
}

func WithMetrics(registry *metrics.Registry) Option {
	return func(s *Service) {
		s.metrics = registry
	}
}

func WithLogger(logger *logging.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

func New(store Store, defaultTimezone string, defaultLocation *time.Location, opts ...Option) *Service {
	if defaultTimezone == "" {
		defaultTimezone = "Europe/Moscow"
	}
	if defaultLocation == nil {
		defaultLocation = time.Local
	}
	svc := &Service{
		store:           store,
		defaultTimezone: defaultTimezone,
		defaultLocation: defaultLocation,
		now:             time.Now,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) RegisterUser(ctx context.Context, tgUser domain.TelegramUser) (*domain.User, *domain.Workspace, error) {
	user, err := s.store.EnsureUser(ctx, tgUser, s.defaultTimezone)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure user: %w", err)
	}
	workspace, err := s.store.EnsurePersonalWorkspace(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("ensure personal workspace: %w", err)
	}
	return user, workspace, nil
}

func (s *Service) CreateTaskFromText(ctx context.Context, tgUser domain.TelegramUser, text string) (*CreateTaskResult, error) {
	user, workspace, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}

	location := s.locationForUser(user)
	parserStart := time.Now()
	parsed, err := parser.Parse(text, s.now().In(location), location)
	s.observeParser(parserStart, err)
	if err != nil {
		s.logError("parser_failed", err, logging.Fields{
			"user_id":      user.ID,
			"workspace_id": workspace.ID,
			"reason":       parserErrorReason(err),
		})
		return nil, err
	}

	status := domain.StatusNew
	if parsed.DueAt != nil {
		status = domain.StatusPlanned
	}
	assigneeID := user.ID
	task := domain.Task{
		WorkspaceID:    workspace.ID,
		CreatorUserID:  user.ID,
		AssigneeUserID: &assigneeID,
		Title:          parsed.Title,
		Status:         status,
		Priority:       parsed.Priority,
		Category:       parsed.Category,
		DueAt:          parsed.DueAt,
		RemindAt:       parsed.RemindAt,
	}
	if err := s.store.CreateTask(ctx, &task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	if task.RemindAt != nil {
		if err := s.store.CreateTaskReminder(ctx, task.ID, *task.RemindAt); err != nil {
			return nil, fmt.Errorf("create task reminder: %w", err)
		}
	}
	if err := s.store.CreateTaskEvent(ctx, task.ID, user.ID, "created", map[string]any{
		"source":     "text",
		"confidence": parsed.Confidence,
		"warnings":   parsed.Warnings,
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	s.incTaskMetric("task_created_total", task, user.ID)
	s.logInfo("task_created", taskLogFields(task, user.ID))

	return &CreateTaskResult{Task: task, Parse: parsed}, nil
}

func (s *Service) MarkDone(ctx context.Context, tgUser domain.TelegramUser, taskID int64) (*domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	task, err := s.store.UpdateTaskStatus(ctx, taskID, user.ID, domain.StatusDone, s.now())
	if err != nil {
		return nil, fmt.Errorf("mark task done: %w", err)
	}
	if err := s.store.CreateTaskEvent(ctx, taskID, user.ID, "done", emptyPayload()); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	s.incTaskMetric("task_done_total", *task, user.ID)
	s.logInfo("done_task", taskLogFields(*task, user.ID))
	return task, nil
}

func (s *Service) Cancel(ctx context.Context, tgUser domain.TelegramUser, taskID int64) (*domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	task, err := s.store.UpdateTaskStatus(ctx, taskID, user.ID, domain.StatusCancelled, s.now())
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}
	if err := s.store.CreateTaskEvent(ctx, taskID, user.ID, "cancelled", emptyPayload()); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	s.incTaskMetric("task_cancelled_total", *task, user.ID)
	s.logInfo("cancel_task", taskLogFields(*task, user.ID))
	return task, nil
}

func (s *Service) Postpone(ctx context.Context, tgUser domain.TelegramUser, taskID int64, option string) (*domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}

	task, err := s.store.TaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	shift, err := postponeShift(option)
	if err != nil {
		return nil, err
	}

	now := s.now().In(s.locationForUser(user))
	var dueAt *time.Time
	if task.DueAt != nil {
		next := task.DueAt.Add(shift)
		dueAt = &next
	} else {
		next := now.Add(shift)
		dueAt = &next
	}

	var remindAt *time.Time
	if task.RemindAt != nil {
		next := task.RemindAt.Add(shift)
		remindAt = &next
	} else if dueAt != nil {
		next := dueAt.Add(-time.Hour)
		if next.Before(now) {
			next = now.Add(5 * time.Minute)
		}
		remindAt = &next
	}

	updated, err := s.store.PostponeTask(ctx, taskID, user.ID, dueAt, remindAt, s.now())
	if err != nil {
		return nil, fmt.Errorf("postpone task: %w", err)
	}
	if remindAt != nil {
		if err := s.store.CreateTaskReminder(ctx, taskID, *remindAt); err != nil {
			return nil, fmt.Errorf("create task reminder: %w", err)
		}
	}
	if err := s.store.CreateTaskEvent(ctx, taskID, user.ID, "postponed", map[string]any{
		"option": option,
		"due_at": nullableTime(dueAt),
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	s.incTaskMetric("task_postponed_total", *updated, user.ID)
	fields := taskLogFields(*updated, user.ID)
	fields["option"] = option
	fields["postponed_count"] = updated.PostponedCount
	s.logInfo("postpone_task", fields)
	return updated, nil
}

func (s *Service) Today(ctx context.Context, tgUser domain.TelegramUser) ([]domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	location := s.locationForUser(user)
	now := s.now().In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 1)
	return s.store.TasksForRange(ctx, user.ID, start, end)
}

func (s *Service) Week(ctx context.Context, tgUser domain.TelegramUser) ([]domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	location := s.locationForUser(user)
	now := s.now().In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 7)
	return s.store.TasksForRange(ctx, user.ID, start, end)
}

func (s *Service) DueReminders(ctx context.Context, now time.Time, limit int) ([]ReminderNotification, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.DueReminderNotifications(ctx, now, limit)
}

func (s *Service) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	return s.store.MarkReminderSent(ctx, reminderID, sentAt)
}

func (s *Service) DueDigests(ctx context.Context, now time.Time, hour int, minute int) ([]DigestNotification, error) {
	users, err := s.store.UsersForDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("users for digest: %w", err)
	}

	result := make([]DigestNotification, 0)
	for _, user := range users {
		location := s.locationForUser(&user)
		localNow := now.In(location)
		if localNow.Hour() != hour || localNow.Minute() != minute {
			continue
		}
		digestDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
		sent, err := s.store.HasDigestRun(ctx, user.ID, digestDate)
		if err != nil {
			return nil, fmt.Errorf("check digest run: %w", err)
		}
		if sent {
			continue
		}
		tasks, err := s.store.TasksForRange(ctx, user.ID, digestDate, digestDate.AddDate(0, 0, 1))
		if err != nil {
			return nil, fmt.Errorf("tasks for digest: %w", err)
		}
		result = append(result, DigestNotification{
			User:       user,
			DigestDate: digestDate,
			Tasks:      tasks,
		})
	}
	return result, nil
}

func (s *Service) MarkDigestSent(ctx context.Context, userID int64, digestDate time.Time, sentAt time.Time) error {
	return s.store.MarkDigestRun(ctx, userID, digestDate, sentAt)
}

func (s *Service) locationForUser(user *domain.User) *time.Location {
	if user != nil && user.Timezone != "" {
		if location, err := time.LoadLocation(user.Timezone); err == nil {
			return location
		}
	}
	return s.defaultLocation
}

func postponeShift(option string) (time.Duration, error) {
	switch option {
	case "tomorrow":
		return 24 * time.Hour, nil
	case "3d":
		return 72 * time.Hour, nil
	case "week":
		return 7 * 24 * time.Hour, nil
	default:
		return 0, ErrUnknownPostponeOption
	}
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

func emptyPayload() json.RawMessage {
	return json.RawMessage(`{}`)
}

func (s *Service) observeParser(start time.Time, err error) {
	if s.metrics == nil {
		return
	}
	s.metrics.ObserveDuration("parser_duration_seconds", nil, start)
	if err != nil {
		s.metrics.Inc("parser_error_total", metrics.Labels{"reason": parserErrorReason(err)})
		return
	}
	s.metrics.Inc("parser_success_total", nil)
}

func (s *Service) incTaskMetric(name string, task domain.Task, userID int64) {
	if s.metrics == nil {
		return
	}
	s.metrics.Inc(name, metrics.Labels{
		"workspace_id": fmt.Sprint(task.WorkspaceID),
		"user_id":      fmt.Sprint(userID),
		"priority":     string(task.Priority),
		"category":     categoryLabel(task.Category),
	})
}

func (s *Service) logInfo(event string, fields logging.Fields) {
	if s.logger != nil {
		s.logger.Info(event, fields)
	}
}

func (s *Service) logError(event string, err error, fields logging.Fields) {
	if s.logger != nil {
		s.logger.Error(event, err, fields)
	}
}

func taskLogFields(task domain.Task, userID int64) logging.Fields {
	return logging.Fields{
		"user_id":      userID,
		"workspace_id": task.WorkspaceID,
		"task_id":      task.ID,
		"priority":     task.Priority,
		"category":     categoryLabel(task.Category),
	}
}

func categoryLabel(category *string) string {
	if category == nil || *category == "" {
		return "none"
	}
	return *category
}

func parserErrorReason(err error) string {
	if errors.Is(err, parser.ErrEmptyTitle) {
		return "empty_title"
	}
	return "parse_error"
}
