package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"planing_bot/internal/assignee"
	"planing_bot/internal/domain"
	"planing_bot/internal/logging"
	"planing_bot/internal/metrics"
	"planing_bot/internal/parser"
)

var (
	ErrUnknownPostponeOption = errors.New("unknown postpone option")
	ErrProfileLinkNotFound   = errors.New("profile link not found")
	ErrProfileLinkNotPending = errors.New("profile link is not pending")
	ErrProfileLinkSelf       = errors.New("cannot link profile to itself")
	ErrProfileAliasesEmpty   = errors.New("profile aliases are empty")
	ErrProfileAliasInUse     = errors.New("profile alias is already used")
	ErrAssigneeNotLinked     = errors.New("assignee is not linked")
)

type Store interface {
	EnsureUser(ctx context.Context, user domain.TelegramUser, defaultTimezone string) (*domain.User, error)
	EnsurePersonalWorkspace(ctx context.Context, userID int64) (*domain.Workspace, error)
	CreateProfileLinkInvite(ctx context.Context, inviterUserID int64, token string, aliases []domain.ProfileLinkAliasInput) (*domain.ProfileLink, error)
	AcceptProfileLinkInvite(ctx context.Context, token string, inviteeUserID int64, aliases []domain.ProfileLinkAliasInput, acceptedAt time.Time) (*domain.ProfileLink, error)
	LinkedProfiles(ctx context.Context, ownerUserID int64) ([]domain.LinkedProfile, error)
	CreateTask(ctx context.Context, task *domain.Task) error
	CreateTaskReminder(ctx context.Context, taskID int64, remindAt time.Time) error
	CreateTaskEvent(ctx context.Context, taskID int64, userID int64, eventType string, payload any) error
	TaskByID(ctx context.Context, taskID int64) (*domain.Task, error)
	TaskRecipient(ctx context.Context, taskID int64) (*domain.User, error)
	UpdateTaskStatus(ctx context.Context, taskID int64, userID int64, status domain.Status, at time.Time) (*domain.Task, error)
	UpdateTaskSchedule(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error)
	PostponeTask(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error)
	TasksForRange(ctx context.Context, userID int64, start time.Time, end time.Time) ([]domain.Task, error)
	DueReminderNotifications(ctx context.Context, now time.Time, limit int) ([]ReminderNotification, error)
	MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error
	MarkTaskRemindersSentBefore(ctx context.Context, taskID int64, before time.Time, sentAt time.Time) error
	UsersForDigest(ctx context.Context) ([]domain.User, error)
	HasDigestRun(ctx context.Context, userID int64, digestDate time.Time) (bool, error)
	MarkDigestRun(ctx context.Context, userID int64, digestDate time.Time, sentAt time.Time) error
}

type Service struct {
	store           Store
	defaultTimezone string
	defaultLocation *time.Location
	parser          textParser
	metrics         *metrics.Registry
	logger          *logging.Logger
	now             func() time.Time
}

type Option func(*Service)

type textParser interface {
	Parse(ctx context.Context, text string, now time.Time, location *time.Location) (parser.ParseResult, error)
}

type ruleTextParser struct{}

type CreateTaskResult struct {
	Task       domain.Task
	Parse      parser.ParseResult
	Creator    domain.User
	Assignee   domain.User
	Resolution assignee.Resolution
}

type ProfileLinkInviteResult struct {
	Link    domain.ProfileLink
	Token   string
	Aliases []string
}

type AssigneeClarificationError struct {
	TaskText string
	Options  []assignee.Option
}

func (e *AssigneeClarificationError) Error() string {
	return "task assignee needs clarification"
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

func WithParser(textParser textParser) Option {
	return func(s *Service) {
		if textParser != nil {
			s.parser = textParser
		}
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
		parser:          ruleTextParser{},
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

func (s *Service) CreateProfileLinkInvite(ctx context.Context, tgUser domain.TelegramUser, aliases []string) (*ProfileLinkInviteResult, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	inputs, expanded := profileAliasInputs(aliases)
	if len(inputs) == 0 {
		return nil, ErrProfileAliasesEmpty
	}
	token, err := generateInviteToken()
	if err != nil {
		return nil, err
	}
	link, err := s.store.CreateProfileLinkInvite(ctx, user.ID, token, inputs)
	if err != nil {
		return nil, fmt.Errorf("create profile link invite: %w", err)
	}
	return &ProfileLinkInviteResult{Link: *link, Token: link.InviteToken, Aliases: expanded}, nil
}

func (s *Service) AcceptProfileLinkInvite(ctx context.Context, tgUser domain.TelegramUser, token string, aliases []string) (*domain.ProfileLink, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	inputs, _ := profileAliasInputs(aliases)
	if len(inputs) == 0 {
		return nil, ErrProfileAliasesEmpty
	}
	link, err := s.store.AcceptProfileLinkInvite(ctx, token, user.ID, inputs, s.now())
	if err != nil {
		return nil, fmt.Errorf("accept profile link invite: %w", err)
	}
	return link, nil
}

func (s *Service) LinkedProfiles(ctx context.Context, tgUser domain.TelegramUser) ([]domain.LinkedProfile, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	return s.store.LinkedProfiles(ctx, user.ID)
}

func (s *Service) CreateTaskFromText(ctx context.Context, tgUser domain.TelegramUser, text string) (*CreateTaskResult, error) {
	user, workspace, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}

	linkedProfiles, err := s.store.LinkedProfiles(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("linked profiles: %w", err)
	}
	resolution := assignee.NewResolver(user.ID, linkedProfileCandidates(linkedProfiles)).Resolve(text)
	if resolution.NeedsClarification {
		return nil, &AssigneeClarificationError{
			TaskText: resolution.TaskText,
			Options:  resolution.Options,
		}
	}
	return s.createTaskForResolvedAssignee(ctx, user, workspace, text, resolution.TaskText, resolution)
}

func (s *Service) CreateTaskForAssignee(ctx context.Context, tgUser domain.TelegramUser, text string, assigneeUserID int64) (*CreateTaskResult, error) {
	user, workspace, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	if assigneeUserID == 0 {
		assigneeUserID = user.ID
	}
	if assigneeUserID != user.ID {
		linkedProfiles, err := s.store.LinkedProfiles(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("linked profiles: %w", err)
		}
		if !isLinkedAssignee(linkedProfiles, assigneeUserID) {
			return nil, ErrAssigneeNotLinked
		}
	}
	resolution := assignee.Resolution{
		AssigneeUserID: assigneeUserID,
		TaskText:       text,
		Source:         assignee.SourceClarification,
	}
	return s.createTaskForResolvedAssignee(ctx, user, workspace, text, text, resolution)
}

func (s *Service) createTaskForResolvedAssignee(ctx context.Context, user *domain.User, workspace *domain.Workspace, inputText string, taskText string, resolution assignee.Resolution) (*CreateTaskResult, error) {
	assigneeUserID := resolution.AssigneeUserID
	if assigneeUserID == 0 {
		assigneeUserID = user.ID
	}
	taskWorkspace := workspace
	if assigneeUserID != user.ID {
		var err error
		taskWorkspace, err = s.store.EnsurePersonalWorkspace(ctx, assigneeUserID)
		if err != nil {
			return nil, fmt.Errorf("ensure assignee workspace: %w", err)
		}
	}

	location := s.locationForUser(user)
	parserStart := time.Now()
	parsed, err := s.parser.Parse(ctx, taskText, s.now().In(location), location)
	s.observeParser(parserStart, err)
	if err != nil {
		s.logError("parser_failed", err, logging.Fields{
			"user_id":      user.ID,
			"workspace_id": taskWorkspace.ID,
			"reason":       parserErrorReason(err),
		})
		return nil, err
	}
	parsed = normalizeParsedSchedule(parsed, s.now().In(location), location)

	status := domain.StatusNew
	if parsed.DueAt != nil {
		status = domain.StatusPlanned
	}
	task := domain.Task{
		WorkspaceID:    taskWorkspace.ID,
		CreatorUserID:  user.ID,
		AssigneeUserID: &assigneeUserID,
		Title:          parsed.Title,
		Status:         status,
		Priority:       parsed.Priority,
		Category:       parsed.Category,
		RecurrenceRule: parsed.RecurrenceRule,
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
		"source":           "text",
		"input_text":       inputText,
		"task_text":        taskText,
		"confidence":       parsed.Confidence,
		"warnings":         parsed.Warnings,
		"assignee_source":  resolution.Source,
		"assignee_alias":   resolution.MatchedAlias,
		"assignee_user_id": assigneeUserID,
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	s.incTaskMetric("task_created_total", task, user.ID)
	s.logInfo("task_created", taskLogFields(task, user.ID))

	assigneeUser := *user
	if assigneeUserID != user.ID {
		recipient, err := s.store.TaskRecipient(ctx, task.ID)
		if err != nil {
			return nil, fmt.Errorf("task recipient: %w", err)
		}
		assigneeUser = *recipient
	}
	return &CreateTaskResult{Task: task, Parse: parsed, Creator: *user, Assignee: assigneeUser, Resolution: resolution}, nil
}

func (ruleTextParser) Parse(_ context.Context, text string, now time.Time, location *time.Location) (parser.ParseResult, error) {
	return parser.Parse(text, now, location)
}

func (s *Service) MarkDone(ctx context.Context, tgUser domain.TelegramUser, taskID int64) (*domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	location := s.locationForUser(user)
	currentTask, err := s.store.TaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	currentTask = taskInLocation(currentTask, location)
	if currentTask.RecurrenceRule != nil {
		now := s.now()
		if currentTask.RemindAt != nil {
			if err := s.store.MarkTaskRemindersSentBefore(ctx, taskID, currentTask.RemindAt.Add(time.Nanosecond), now); err != nil {
				return nil, fmt.Errorf("mark current recurring reminders sent: %w", err)
			}
		}
		task, err := s.scheduleNextRecurringTask(ctx, *currentTask, user.ID, now, "done")
		if err != nil {
			return nil, err
		}
		if err := s.store.CreateTaskEvent(ctx, taskID, user.ID, "recurring_done", emptyPayload()); err != nil {
			return nil, fmt.Errorf("create task event: %w", err)
		}
		task = taskInLocation(task, location)
		s.incTaskMetric("task_done_total", *task, user.ID)
		s.logInfo("done_task", taskLogFields(*task, user.ID))
		return task, nil
	}
	task, err := s.store.UpdateTaskStatus(ctx, taskID, user.ID, domain.StatusDone, s.now())
	if err != nil {
		return nil, fmt.Errorf("mark task done: %w", err)
	}
	task = taskInLocation(task, location)
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
	location := s.locationForUser(user)
	task, err := s.store.UpdateTaskStatus(ctx, taskID, user.ID, domain.StatusCancelled, s.now())
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}
	task = taskInLocation(task, location)
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
	location := s.locationForUser(user)

	task, err := s.store.TaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	task = taskInLocation(task, location)
	shift, err := postponeShift(option)
	if err != nil {
		return nil, err
	}

	now := s.now().In(location)
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
	updated = taskInLocation(updated, location)
	if remindAt != nil {
		if err := s.store.MarkTaskRemindersSentBefore(ctx, taskID, remindAt.Add(time.Nanosecond), now); err != nil {
			return nil, fmt.Errorf("mark previous reminders sent: %w", err)
		}
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

func (s *Service) PostponeReminder(ctx context.Context, tgUser domain.TelegramUser, taskID int64, option string) (*domain.Task, error) {
	user, _, err := s.RegisterUser(ctx, tgUser)
	if err != nil {
		return nil, err
	}
	location := s.locationForUser(user)
	task, err := s.store.TaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	task = taskInLocation(task, location)
	shift, err := postponeShift(option)
	if err != nil {
		return nil, err
	}

	now := s.now().In(location)
	base := now
	if task.RemindAt != nil && task.RemindAt.After(now) {
		base = *task.RemindAt
	}
	remindAt := base.Add(shift)
	updated, err := s.store.UpdateTaskSchedule(ctx, taskID, user.ID, task.DueAt, &remindAt, s.now())
	if err != nil {
		return nil, fmt.Errorf("postpone reminder: %w", err)
	}
	updated = taskInLocation(updated, location)
	if err := s.store.MarkTaskRemindersSentBefore(ctx, taskID, remindAt.Add(time.Nanosecond), now); err != nil {
		return nil, fmt.Errorf("mark previous reminders sent: %w", err)
	}
	if err := s.store.CreateTaskReminder(ctx, taskID, remindAt); err != nil {
		return nil, fmt.Errorf("create task reminder: %w", err)
	}
	if err := s.store.CreateTaskEvent(ctx, taskID, user.ID, "reminder_postponed", map[string]any{
		"option":    option,
		"remind_at": nullableTime(&remindAt),
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	fields := taskLogFields(*updated, user.ID)
	fields["option"] = option
	s.logInfo("postpone_reminder", fields)
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
	tasks, err := s.store.TasksForRange(ctx, user.ID, start, end)
	if err != nil {
		return nil, err
	}
	return tasksInLocation(tasks, location), nil
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
	tasks, err := s.store.TasksForRange(ctx, user.ID, start, end)
	if err != nil {
		return nil, err
	}
	return tasksInLocation(tasks, location), nil
}

func (s *Service) DueReminders(ctx context.Context, now time.Time, limit int) ([]ReminderNotification, error) {
	if limit <= 0 {
		limit = 100
	}
	notifications, err := s.store.DueReminderNotifications(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	for i := range notifications {
		location := s.locationForUser(&notifications[i].User)
		notifications[i].Task = *taskInLocation(&notifications[i].Task, location)
		notifications[i].Reminder = reminderInLocation(notifications[i].Reminder, location)
	}
	return notifications, nil
}

func (s *Service) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	return s.store.MarkReminderSent(ctx, reminderID, sentAt)
}

func (s *Service) ScheduleNextRecurringReminder(ctx context.Context, notification ReminderNotification, sentAt time.Time) (*domain.Task, error) {
	if notification.Task.RecurrenceRule == nil {
		return &notification.Task, nil
	}
	task, err := s.scheduleNextRecurringTask(ctx, notification.Task, notification.User.ID, sentAt, "reminder_sent")
	if err != nil {
		return nil, err
	}
	if err := s.store.CreateTaskEvent(ctx, notification.Task.ID, notification.User.ID, "recurrence_scheduled", map[string]any{
		"source":    "reminder_sent",
		"remind_at": nullableTime(task.RemindAt),
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	return task, nil
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
			Tasks:      tasksInLocation(tasks, location),
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

func normalizeParsedSchedule(parsed parser.ParseResult, now time.Time, location *time.Location) parser.ParseResult {
	if location == nil {
		location = time.Local
	}
	now = now.In(location)
	if parsed.DueAt != nil {
		due := parsed.DueAt.In(location)
		parsed.DueAt = &due
	}
	if parsed.RemindAt != nil {
		remind := parsed.RemindAt.In(location)
		if remind.Before(now) && (parsed.DueAt == nil || parsed.DueAt.After(now)) {
			remind = now.Add(5 * time.Minute)
			if parsed.DueAt != nil && remind.After(*parsed.DueAt) {
				remind = *parsed.DueAt
			}
			parsed.Warnings = append(parsed.Warnings, "remind_at_adjusted_from_past")
		}
		parsed.RemindAt = &remind
	}
	return parsed
}

func taskInLocation(task *domain.Task, location *time.Location) *domain.Task {
	if task == nil || location == nil {
		return task
	}
	if task.DueAt != nil {
		due := task.DueAt.In(location)
		task.DueAt = &due
	}
	if task.RemindAt != nil {
		remind := task.RemindAt.In(location)
		task.RemindAt = &remind
	}
	if task.DoneAt != nil {
		done := task.DoneAt.In(location)
		task.DoneAt = &done
	}
	if task.CancelledAt != nil {
		cancelled := task.CancelledAt.In(location)
		task.CancelledAt = &cancelled
	}
	if !task.CreatedAt.IsZero() {
		task.CreatedAt = task.CreatedAt.In(location)
	}
	if !task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.UpdatedAt.In(location)
	}
	return task
}

func tasksInLocation(tasks []domain.Task, location *time.Location) []domain.Task {
	for i := range tasks {
		taskInLocation(&tasks[i], location)
	}
	return tasks
}

func reminderInLocation(reminder domain.TaskReminder, location *time.Location) domain.TaskReminder {
	if location == nil {
		return reminder
	}
	reminder.RemindAt = reminder.RemindAt.In(location)
	if reminder.SentAt != nil {
		sent := reminder.SentAt.In(location)
		reminder.SentAt = &sent
	}
	if !reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = reminder.CreatedAt.In(location)
	}
	return reminder
}

func postponeShift(option string) (time.Duration, error) {
	switch option {
	case "1h":
		return time.Hour, nil
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

func (s *Service) scheduleNextRecurringTask(ctx context.Context, task domain.Task, userID int64, at time.Time, source string) (*domain.Task, error) {
	nextRemindAt, nextDueAt, err := nextRecurringSchedule(task, at, s.defaultLocation)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateTaskSchedule(ctx, task.ID, userID, &nextDueAt, &nextRemindAt, at)
	if err != nil {
		return nil, fmt.Errorf("update recurring task schedule: %w", err)
	}
	if err := s.store.CreateTaskReminder(ctx, task.ID, nextRemindAt); err != nil {
		return nil, fmt.Errorf("create next recurring reminder: %w", err)
	}
	if err := s.store.CreateTaskEvent(ctx, task.ID, userID, "recurrence_advanced", map[string]any{
		"source":    source,
		"remind_at": nextRemindAt.Format(time.RFC3339),
		"due_at":    nextDueAt.Format(time.RFC3339),
	}); err != nil {
		return nil, fmt.Errorf("create task event: %w", err)
	}
	return updated, nil
}

func nextRecurringSchedule(task domain.Task, at time.Time, fallbackLocation *time.Location) (time.Time, time.Time, error) {
	if task.RecurrenceRule == nil {
		return time.Time{}, time.Time{}, errors.New("task is not recurring")
	}
	location := fallbackLocation
	if task.RemindAt != nil {
		location = task.RemindAt.Location()
	}
	base := at.In(location)
	if task.RemindAt != nil {
		base = task.RemindAt.In(location)
	}

	var nextRemind time.Time
	switch *task.RecurrenceRule {
	case domain.RecurrenceDaily:
		nextRemind = base.AddDate(0, 0, 1)
		for !nextRemind.After(at.In(location)) {
			nextRemind = nextRemind.AddDate(0, 0, 1)
		}
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported recurrence rule %q", *task.RecurrenceRule)
	}
	nextDue := time.Date(nextRemind.Year(), nextRemind.Month(), nextRemind.Day(), 23, 59, 0, 0, location)
	if task.DueAt != nil {
		due := task.DueAt.In(location)
		nextDue = time.Date(nextRemind.Year(), nextRemind.Month(), nextRemind.Day(), due.Hour(), due.Minute(), due.Second(), due.Nanosecond(), location)
		if nextDue.Before(nextRemind) {
			nextDue = time.Date(nextRemind.Year(), nextRemind.Month(), nextRemind.Day(), 23, 59, 0, 0, location)
		}
	}
	return nextRemind, nextDue, nil
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

func profileAliasInputs(values []string) ([]domain.ProfileLinkAliasInput, []string) {
	manual := make(map[string]struct{})
	for _, value := range values {
		for _, alias := range strings.Split(value, ",") {
			normalized := assignee.NormalizeText(alias)
			if normalized != "" {
				manual[normalized] = struct{}{}
			}
		}
	}
	expanded := assignee.NormalizeAliases(values)
	inputs := make([]domain.ProfileLinkAliasInput, 0, len(expanded))
	for _, alias := range expanded {
		source := domain.ProfileLinkAliasGenerated
		if _, ok := manual[alias]; ok {
			source = domain.ProfileLinkAliasManual
		}
		inputs = append(inputs, domain.ProfileLinkAliasInput{
			Alias:           alias,
			NormalizedAlias: alias,
			Source:          source,
		})
	}
	return inputs, expanded
}

func linkedProfileCandidates(profiles []domain.LinkedProfile) []assignee.Candidate {
	candidates := make([]assignee.Candidate, 0, len(profiles))
	for _, profile := range profiles {
		candidates = append(candidates, assignee.Candidate{
			UserID:  profile.User.ID,
			Name:    displayUserName(profile.User),
			Aliases: profile.Aliases,
		})
	}
	return candidates
}

func isLinkedAssignee(profiles []domain.LinkedProfile, userID int64) bool {
	for _, profile := range profiles {
		if profile.User.ID == userID {
			return true
		}
	}
	return false
}

func displayUserName(user domain.User) string {
	switch {
	case user.FirstName != "" && user.LastName != "":
		return user.FirstName + " " + user.LastName
	case user.FirstName != "":
		return user.FirstName
	case user.Username != "":
		return user.Username
	default:
		return fmt.Sprintf("user-%d", user.ID)
	}
}

func generateInviteToken() (string, error) {
	var data [18]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}
