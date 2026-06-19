package service

import (
	"context"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/metrics"
)

type ObservedStore struct {
	next    Store
	metrics *metrics.Registry
}

func NewObservedStore(next Store, registry *metrics.Registry) *ObservedStore {
	return &ObservedStore{next: next, metrics: registry}
}

func (s *ObservedStore) EnsureUser(ctx context.Context, user domain.TelegramUser, defaultTimezone string) (*domain.User, error) {
	start := time.Now()
	result, err := s.next.EnsureUser(ctx, user, defaultTimezone)
	s.observe("ensure_user", start, err)
	return result, err
}

func (s *ObservedStore) EnsurePersonalWorkspace(ctx context.Context, userID int64) (*domain.Workspace, error) {
	start := time.Now()
	result, err := s.next.EnsurePersonalWorkspace(ctx, userID)
	s.observe("ensure_personal_workspace", start, err)
	return result, err
}

func (s *ObservedStore) CreateProfileLinkInvite(ctx context.Context, inviterUserID int64, token string, aliases []domain.ProfileLinkAliasInput) (*domain.ProfileLink, error) {
	start := time.Now()
	result, err := s.next.CreateProfileLinkInvite(ctx, inviterUserID, token, aliases)
	s.observe("create_profile_link_invite", start, err)
	return result, err
}

func (s *ObservedStore) AcceptProfileLinkInvite(ctx context.Context, token string, inviteeUserID int64, aliases []domain.ProfileLinkAliasInput, acceptedAt time.Time) (*domain.ProfileLink, error) {
	start := time.Now()
	result, err := s.next.AcceptProfileLinkInvite(ctx, token, inviteeUserID, aliases, acceptedAt)
	s.observe("accept_profile_link_invite", start, err)
	return result, err
}

func (s *ObservedStore) LinkedProfiles(ctx context.Context, ownerUserID int64) ([]domain.LinkedProfile, error) {
	start := time.Now()
	result, err := s.next.LinkedProfiles(ctx, ownerUserID)
	s.observe("linked_profiles", start, err)
	return result, err
}

func (s *ObservedStore) CreateTask(ctx context.Context, task *domain.Task) error {
	start := time.Now()
	err := s.next.CreateTask(ctx, task)
	s.observe("create_task", start, err)
	return err
}

func (s *ObservedStore) CreateTaskReminder(ctx context.Context, taskID int64, remindAt time.Time) error {
	start := time.Now()
	err := s.next.CreateTaskReminder(ctx, taskID, remindAt)
	s.observe("create_task_reminder", start, err)
	return err
}

func (s *ObservedStore) CreateTaskEvent(ctx context.Context, taskID int64, userID int64, eventType string, payload any) error {
	start := time.Now()
	err := s.next.CreateTaskEvent(ctx, taskID, userID, eventType, payload)
	s.observe("create_task_event", start, err)
	return err
}

func (s *ObservedStore) TaskByID(ctx context.Context, taskID int64) (*domain.Task, error) {
	start := time.Now()
	result, err := s.next.TaskByID(ctx, taskID)
	s.observe("task_by_id", start, err)
	return result, err
}

func (s *ObservedStore) TaskRecipient(ctx context.Context, taskID int64) (*domain.User, error) {
	start := time.Now()
	result, err := s.next.TaskRecipient(ctx, taskID)
	s.observe("task_recipient", start, err)
	return result, err
}

func (s *ObservedStore) UpdateTaskStatus(ctx context.Context, taskID int64, userID int64, status domain.Status, at time.Time) (*domain.Task, error) {
	start := time.Now()
	result, err := s.next.UpdateTaskStatus(ctx, taskID, userID, status, at)
	s.observe("update_task_status", start, err)
	return result, err
}

func (s *ObservedStore) UpdateTaskSchedule(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	start := time.Now()
	result, err := s.next.UpdateTaskSchedule(ctx, taskID, userID, dueAt, remindAt, at)
	s.observe("update_task_schedule", start, err)
	return result, err
}

func (s *ObservedStore) PostponeTask(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	start := time.Now()
	result, err := s.next.PostponeTask(ctx, taskID, userID, dueAt, remindAt, at)
	s.observe("postpone_task", start, err)
	return result, err
}

func (s *ObservedStore) TasksForRange(ctx context.Context, userID int64, startAt time.Time, endAt time.Time) ([]domain.Task, error) {
	start := time.Now()
	result, err := s.next.TasksForRange(ctx, userID, startAt, endAt)
	s.observe("tasks_for_range", start, err)
	return result, err
}

func (s *ObservedStore) DueReminderNotifications(ctx context.Context, now time.Time, limit int) ([]ReminderNotification, error) {
	start := time.Now()
	result, err := s.next.DueReminderNotifications(ctx, now, limit)
	s.observe("due_reminder_notifications", start, err)
	return result, err
}

func (s *ObservedStore) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	start := time.Now()
	err := s.next.MarkReminderSent(ctx, reminderID, sentAt)
	s.observe("mark_reminder_sent", start, err)
	return err
}

func (s *ObservedStore) MarkTaskRemindersSentBefore(ctx context.Context, taskID int64, before time.Time, sentAt time.Time) error {
	start := time.Now()
	err := s.next.MarkTaskRemindersSentBefore(ctx, taskID, before, sentAt)
	s.observe("mark_task_reminders_sent_before", start, err)
	return err
}

func (s *ObservedStore) UsersForDigest(ctx context.Context) ([]domain.User, error) {
	start := time.Now()
	result, err := s.next.UsersForDigest(ctx)
	s.observe("users_for_digest", start, err)
	return result, err
}

func (s *ObservedStore) HasDigestRun(ctx context.Context, userID int64, digestDate time.Time) (bool, error) {
	start := time.Now()
	result, err := s.next.HasDigestRun(ctx, userID, digestDate)
	s.observe("has_digest_run", start, err)
	return result, err
}

func (s *ObservedStore) MarkDigestRun(ctx context.Context, userID int64, digestDate time.Time, sentAt time.Time) error {
	start := time.Now()
	err := s.next.MarkDigestRun(ctx, userID, digestDate, sentAt)
	s.observe("mark_digest_run", start, err)
	return err
}

func (s *ObservedStore) observe(operation string, start time.Time, err error) {
	if s.metrics == nil {
		return
	}
	labels := metrics.Labels{"operation": operation}
	s.metrics.ObserveDuration("storage_query_duration_seconds", labels, start)
	if err != nil {
		s.metrics.Inc("storage_error_total", labels)
	}
}
