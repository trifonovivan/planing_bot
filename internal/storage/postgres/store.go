package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/metrics"
	"planing_bot/internal/service"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) EnsureUser(ctx context.Context, tgUser domain.TelegramUser, defaultTimezone string) (*domain.User, error) {
	const query = `
INSERT INTO users (telegram_id, username, first_name, last_name, timezone)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (telegram_id) DO UPDATE SET
    username = EXCLUDED.username,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    updated_at = now()
RETURNING id, telegram_id, username, first_name, last_name, timezone, created_at, updated_at`

	user := &domain.User{}
	err := s.db.QueryRowContext(ctx, query,
		tgUser.TelegramID,
		tgUser.Username,
		tgUser.FirstName,
		tgUser.LastName,
		defaultTimezone,
	).Scan(&user.ID, &user.TelegramID, &user.Username, &user.FirstName, &user.LastName, &user.Timezone, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) EnsurePersonalWorkspace(ctx context.Context, userID int64) (*domain.Workspace, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	workspace := &domain.Workspace{}
	err = tx.QueryRowContext(ctx, `
SELECT id, name, owner_user_id, created_at, updated_at
FROM workspaces
WHERE owner_user_id = $1 AND name = 'Personal'
ORDER BY id
LIMIT 1`, userID).Scan(&workspace.ID, &workspace.Name, &workspace.OwnerUserID, &workspace.CreatedAt, &workspace.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
INSERT INTO workspaces (name, owner_user_id)
VALUES ('Personal', $1)
RETURNING id, name, owner_user_id, created_at, updated_at`, userID).Scan(&workspace.ID, &workspace.Name, &workspace.OwnerUserID, &workspace.CreatedAt, &workspace.UpdatedAt)
	}
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id) DO NOTHING`, workspace.ID, userID, domain.RoleOwner); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return workspace, nil
}

func (s *Store) CreateTask(ctx context.Context, task *domain.Task) error {
	const query = `
INSERT INTO tasks (
    workspace_id, creator_user_id, assignee_user_id, title, description, status, priority,
    category, recurrence_rule, due_at, remind_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, created_at, updated_at`
	return s.db.QueryRowContext(ctx, query,
		task.WorkspaceID,
		task.CreatorUserID,
		task.AssigneeUserID,
		task.Title,
		task.Description,
		task.Status,
		task.Priority,
		task.Category,
		task.RecurrenceRule,
		task.DueAt,
		task.RemindAt,
	).Scan(&task.ID, &task.CreatedAt, &task.UpdatedAt)
}

func (s *Store) CreateTaskReminder(ctx context.Context, taskID int64, remindAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO task_reminders (task_id, remind_at)
VALUES ($1, $2)`, taskID, remindAt)
	return err
}

func (s *Store) CreateTaskEvent(ctx context.Context, taskID int64, userID int64, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO task_events (task_id, user_id, event_type, payload)
VALUES ($1, $2, $3, $4::jsonb)`, taskID, userID, eventType, string(data))
	return err
}

func (s *Store) TaskByID(ctx context.Context, taskID int64) (*domain.Task, error) {
	row := s.db.QueryRowContext(ctx, taskSelectSQL(`WHERE t.id = $1`), taskID)
	return scanTask(row)
}

func (s *Store) TaskRecipient(ctx context.Context, taskID int64) (*domain.User, error) {
	const query = `
SELECT u.id, u.telegram_id, u.username, u.first_name, u.last_name, u.timezone, u.created_at, u.updated_at
FROM tasks t
JOIN users u ON u.id = COALESCE(t.assignee_user_id, t.creator_user_id)
WHERE t.id = $1`
	user := &domain.User{}
	err := s.db.QueryRowContext(ctx, query, taskID).Scan(
		&user.ID,
		&user.TelegramID,
		&user.Username,
		&user.FirstName,
		&user.LastName,
		&user.Timezone,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) UpdateTaskStatus(ctx context.Context, taskID int64, userID int64, status domain.Status, at time.Time) (*domain.Task, error) {
	const query = `
UPDATE tasks
SET status = $3,
    updated_at = $4,
    done_at = CASE WHEN $3 = 'done' THEN $4 ELSE done_at END,
    cancelled_at = CASE WHEN $3 = 'cancelled' THEN $4 ELSE cancelled_at END
WHERE id = $1
  AND (creator_user_id = $2 OR assignee_user_id = $2)
RETURNING id`
	var id int64
	if err := s.db.QueryRowContext(ctx, query, taskID, userID, status, at).Scan(&id); err != nil {
		return nil, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) UpdateTaskSchedule(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	const query = `
UPDATE tasks
SET due_at = $3,
    remind_at = $4,
    updated_at = $5
WHERE id = $1
  AND (creator_user_id = $2 OR assignee_user_id = $2)
RETURNING id`
	var id int64
	if err := s.db.QueryRowContext(ctx, query, taskID, userID, dueAt, remindAt, at).Scan(&id); err != nil {
		return nil, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) PostponeTask(ctx context.Context, taskID int64, userID int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	const query = `
UPDATE tasks
SET status = 'postponed',
    due_at = $3,
    remind_at = $4,
    postponed_count = postponed_count + 1,
    updated_at = $5
WHERE id = $1
  AND (creator_user_id = $2 OR assignee_user_id = $2)
RETURNING id`
	var id int64
	if err := s.db.QueryRowContext(ctx, query, taskID, userID, dueAt, remindAt, at).Scan(&id); err != nil {
		return nil, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) TasksForRange(ctx context.Context, userID int64, start time.Time, end time.Time) ([]domain.Task, error) {
	const query = `
SELECT
    t.id, t.workspace_id, t.creator_user_id, t.assignee_user_id, t.title, t.description,
    t.status, t.priority, t.category, t.recurrence_rule, t.due_at, t.remind_at, t.postponed_count,
    t.created_at, t.updated_at, t.done_at, t.cancelled_at
FROM tasks t
JOIN workspaces w ON w.id = t.workspace_id
WHERE t.due_at >= $2
  AND t.due_at < $3
  AND t.status IN ('new', 'planned', 'postponed')
  AND w.owner_user_id = $1
  AND (t.assignee_user_id = $1 OR t.assignee_user_id IS NULL)
ORDER BY t.priority ASC, t.due_at ASC, t.id ASC`
	rows, err := s.db.QueryContext(ctx, query, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]domain.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, rows.Err()
}

func (s *Store) DueReminderNotifications(ctx context.Context, now time.Time, limit int) ([]service.ReminderNotification, error) {
	const query = `
SELECT
    r.id, r.task_id, r.remind_at, r.sent_at, r.created_at,
    t.id, t.workspace_id, t.creator_user_id, t.assignee_user_id, t.title, t.description,
    t.status, t.priority, t.category, t.recurrence_rule, t.due_at, t.remind_at, t.postponed_count,
    t.created_at, t.updated_at, t.done_at, t.cancelled_at,
    u.id, u.telegram_id, u.username, u.first_name, u.last_name, u.timezone, u.created_at, u.updated_at
FROM task_reminders r
JOIN tasks t ON t.id = r.task_id
JOIN users u ON u.id = COALESCE(t.assignee_user_id, t.creator_user_id)
WHERE r.sent_at IS NULL
  AND r.remind_at <= $1
  AND t.status IN ('new', 'planned', 'postponed')
ORDER BY r.remind_at ASC
LIMIT $2`
	rows, err := s.db.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notifications := make([]service.ReminderNotification, 0)
	for rows.Next() {
		var reminder domain.TaskReminder
		task, user, err := scanReminderNotification(rows, &reminder)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, service.ReminderNotification{
			Reminder: reminder,
			Task:     *task,
			User:     *user,
		})
	}
	return notifications, rows.Err()
}

func (s *Store) MarkReminderSent(ctx context.Context, reminderID int64, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE task_reminders
SET sent_at = $2
WHERE id = $1 AND sent_at IS NULL`, reminderID, sentAt)
	return err
}

func (s *Store) MarkTaskRemindersSentBefore(ctx context.Context, taskID int64, before time.Time, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE task_reminders
SET sent_at = $3
WHERE task_id = $1
  AND remind_at < $2
  AND sent_at IS NULL`, taskID, before, sentAt)
	return err
}

func (s *Store) UsersForDigest(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, telegram_id, username, first_name, last_name, timezone, created_at, updated_at
FROM users
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]domain.User, 0)
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.TelegramID, &user.Username, &user.FirstName, &user.LastName, &user.Timezone, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) HasDigestRun(ctx context.Context, userID int64, digestDate time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM user_digest_runs WHERE user_id = $1 AND digest_date = $2
)`, userID, digestDate.Format("2006-01-02")).Scan(&exists)
	return exists, err
}

func (s *Store) MarkDigestRun(ctx context.Context, userID int64, digestDate time.Time, sentAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO user_digest_runs (user_id, digest_date, sent_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, digest_date) DO NOTHING`, userID, digestDate.Format("2006-01-02"), sentAt)
	return err
}

func (s *Store) CollectMetrics(ctx context.Context) ([]metrics.GaugeSample, error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.AddDate(0, 0, 1)

	samples := make([]metrics.GaugeSample, 0)
	active, err := s.countTasksByWorkspace(ctx, `
SELECT workspace_id, count(*)
FROM tasks
WHERE status IN ('new', 'planned', 'postponed')
GROUP BY workspace_id`)
	if err != nil {
		return nil, err
	}
	for workspaceID, count := range active {
		samples = append(samples, metrics.GaugeSample{
			Name:   "tasks_active_total",
			Labels: metrics.Labels{"workspace_id": fmt.Sprint(workspaceID)},
			Value:  float64(count),
		})
	}

	overdue, err := s.countTasksByWorkspace(ctx, `
SELECT workspace_id, count(*)
FROM tasks
WHERE status IN ('new', 'planned', 'postponed')
  AND due_at IS NOT NULL
  AND due_at < $1
GROUP BY workspace_id`, now)
	if err != nil {
		return nil, err
	}
	for workspaceID, count := range overdue {
		samples = append(samples, metrics.GaugeSample{
			Name:   "tasks_overdue_total",
			Labels: metrics.Labels{"workspace_id": fmt.Sprint(workspaceID)},
			Value:  float64(count),
		})
	}

	dueToday, err := s.countTasksByWorkspace(ctx, `
SELECT workspace_id, count(*)
FROM tasks
WHERE status IN ('new', 'planned', 'postponed')
  AND due_at >= $1
  AND due_at < $2
GROUP BY workspace_id`, startOfDay, endOfDay)
	if err != nil {
		return nil, err
	}
	for workspaceID, count := range dueToday {
		samples = append(samples, metrics.GaugeSample{
			Name:   "tasks_due_today_total",
			Labels: metrics.Labels{"workspace_id": fmt.Sprint(workspaceID)},
			Value:  float64(count),
		})
	}

	pendingReminders, err := s.countPendingReminders(ctx)
	if err != nil {
		return nil, err
	}
	samples = append(samples, metrics.GaugeSample{
		Name:  "reminders_pending_total",
		Value: float64(pendingReminders),
	})
	return samples, nil
}

func (s *Store) countTasksByWorkspace(ctx context.Context, query string, args ...any) (map[int64]int64, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]int64)
	for rows.Next() {
		var workspaceID int64
		var count int64
		if err := rows.Scan(&workspaceID, &count); err != nil {
			return nil, err
		}
		result[workspaceID] = count
	}
	return result, rows.Err()
}

func (s *Store) countPendingReminders(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM task_reminders r
JOIN tasks t ON t.id = r.task_id
WHERE r.sent_at IS NULL
  AND t.status IN ('new', 'planned', 'postponed')`).Scan(&count)
	return count, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func taskSelectSQL(where string) string {
	return fmt.Sprintf(`
SELECT
    t.id, t.workspace_id, t.creator_user_id, t.assignee_user_id, t.title, t.description,
    t.status, t.priority, t.category, t.recurrence_rule, t.due_at, t.remind_at, t.postponed_count,
    t.created_at, t.updated_at, t.done_at, t.cancelled_at
FROM tasks t
%s`, where)
}

func scanTask(scanner rowScanner) (*domain.Task, error) {
	var task domain.Task
	var assigneeID sql.NullInt64
	var description sql.NullString
	var category sql.NullString
	var recurrenceRule sql.NullString
	var dueAt sql.NullTime
	var remindAt sql.NullTime
	var doneAt sql.NullTime
	var cancelledAt sql.NullTime

	if err := scanner.Scan(
		&task.ID,
		&task.WorkspaceID,
		&task.CreatorUserID,
		&assigneeID,
		&task.Title,
		&description,
		&task.Status,
		&task.Priority,
		&category,
		&recurrenceRule,
		&dueAt,
		&remindAt,
		&task.PostponedCount,
		&task.CreatedAt,
		&task.UpdatedAt,
		&doneAt,
		&cancelledAt,
	); err != nil {
		return nil, err
	}

	if assigneeID.Valid {
		task.AssigneeUserID = &assigneeID.Int64
	}
	if description.Valid {
		task.Description = &description.String
	}
	if category.Valid {
		task.Category = &category.String
	}
	if recurrenceRule.Valid {
		rule := domain.RecurrenceRule(recurrenceRule.String)
		task.RecurrenceRule = &rule
	}
	if dueAt.Valid {
		task.DueAt = &dueAt.Time
	}
	if remindAt.Valid {
		task.RemindAt = &remindAt.Time
	}
	if doneAt.Valid {
		task.DoneAt = &doneAt.Time
	}
	if cancelledAt.Valid {
		task.CancelledAt = &cancelledAt.Time
	}
	return &task, nil
}

func scanReminderNotification(scanner rowScanner, reminder *domain.TaskReminder) (*domain.Task, *domain.User, error) {
	var reminderSentAt sql.NullTime
	task := domain.Task{}
	user := domain.User{}
	var assigneeID sql.NullInt64
	var description sql.NullString
	var category sql.NullString
	var recurrenceRule sql.NullString
	var dueAt sql.NullTime
	var taskRemindAt sql.NullTime
	var doneAt sql.NullTime
	var cancelledAt sql.NullTime

	if err := scanner.Scan(
		&reminder.ID,
		&reminder.TaskID,
		&reminder.RemindAt,
		&reminderSentAt,
		&reminder.CreatedAt,
		&task.ID,
		&task.WorkspaceID,
		&task.CreatorUserID,
		&assigneeID,
		&task.Title,
		&description,
		&task.Status,
		&task.Priority,
		&category,
		&recurrenceRule,
		&dueAt,
		&taskRemindAt,
		&task.PostponedCount,
		&task.CreatedAt,
		&task.UpdatedAt,
		&doneAt,
		&cancelledAt,
		&user.ID,
		&user.TelegramID,
		&user.Username,
		&user.FirstName,
		&user.LastName,
		&user.Timezone,
		&user.CreatedAt,
		&user.UpdatedAt,
	); err != nil {
		return nil, nil, err
	}

	if reminderSentAt.Valid {
		reminder.SentAt = &reminderSentAt.Time
	}
	if assigneeID.Valid {
		task.AssigneeUserID = &assigneeID.Int64
	}
	if description.Valid {
		task.Description = &description.String
	}
	if category.Valid {
		task.Category = &category.String
	}
	if recurrenceRule.Valid {
		rule := domain.RecurrenceRule(recurrenceRule.String)
		task.RecurrenceRule = &rule
	}
	if dueAt.Valid {
		task.DueAt = &dueAt.Time
	}
	if taskRemindAt.Valid {
		task.RemindAt = &taskRemindAt.Time
	}
	if doneAt.Valid {
		task.DoneAt = &doneAt.Time
	}
	if cancelledAt.Valid {
		task.CancelledAt = &cancelledAt.Time
	}
	return &task, &user, nil
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
