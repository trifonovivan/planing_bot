package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/metrics"
)

func TestServiceTaskFlow(t *testing.T) {
	ctx := context.Background()
	loc := mustLocation(t)
	store := newFakeStore()
	registry := metrics.NewRegistry()
	svc := New(store, "Europe/Moscow", loc, WithMetrics(registry))
	svc.now = func() time.Time {
		return time.Date(2026, 6, 19, 10, 0, 0, 0, loc)
	}

	user := domain.TelegramUser{TelegramID: 1001, Username: "ivan"}
	created, err := svc.CreateTaskFromText(ctx, user, "сегодня в 18:00 полить огурцы")
	if err != nil {
		t.Fatalf("CreateTaskFromText error: %v", err)
	}
	if created.Task.ID == 0 {
		t.Fatal("task id is empty")
	}
	if created.Task.Title != "полить огурцы" {
		t.Fatalf("title = %q", created.Task.Title)
	}

	today, err := svc.Today(ctx, user)
	if err != nil {
		t.Fatalf("Today error: %v", err)
	}
	if len(today) != 1 || today[0].ID != created.Task.ID {
		t.Fatalf("today = %+v, want created task", today)
	}

	done, err := svc.MarkDone(ctx, user, created.Task.ID)
	if err != nil {
		t.Fatalf("MarkDone error: %v", err)
	}
	if done.Status != domain.StatusDone || done.DoneAt == nil {
		t.Fatalf("done task = %+v", done)
	}
	today, err = svc.Today(ctx, user)
	if err != nil {
		t.Fatalf("Today after done error: %v", err)
	}
	if len(today) != 0 {
		t.Fatalf("today after done = %+v, want empty", today)
	}

	postponedBase, err := svc.CreateTaskFromText(ctx, user, "завтра в 10 созвон")
	if err != nil {
		t.Fatalf("CreateTaskFromText postponed base error: %v", err)
	}
	postponed, err := svc.Postpone(ctx, user, postponedBase.Task.ID, "3d")
	if err != nil {
		t.Fatalf("Postpone error: %v", err)
	}
	if postponed.Status != domain.StatusPostponed {
		t.Fatalf("postponed status = %s", postponed.Status)
	}
	if postponed.PostponedCount != 1 {
		t.Fatalf("postponed_count = %d, want 1", postponed.PostponedCount)
	}
	wantDue := time.Date(2026, 6, 23, 10, 0, 0, 0, loc)
	if postponed.DueAt == nil || !postponed.DueAt.Equal(wantDue) {
		t.Fatalf("postponed due = %v, want %v", postponed.DueAt, wantDue)
	}

	cancelBase, err := svc.CreateTaskFromText(ctx, user, "завтра купить корм")
	if err != nil {
		t.Fatalf("CreateTaskFromText cancel base error: %v", err)
	}
	if _, err := svc.Cancel(ctx, user, cancelBase.Task.ID); err != nil {
		t.Fatalf("Cancel error: %v", err)
	}

	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"task_created_total",
		"task_done_total",
		"task_postponed_total",
		"task_cancelled_total",
		"parser_success_total 3",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body does not contain %q:\n%s", want, body)
		}
	}
}

func TestRecurringTaskDoneAdvancesScheduleWithoutClosingTask(t *testing.T) {
	ctx := context.Background()
	loc := mustLocation(t)
	store := newFakeStore()
	svc := New(store, "Europe/Moscow", loc)
	svc.now = func() time.Time {
		return time.Date(2026, 6, 19, 23, 23, 0, 0, loc)
	}

	user := domain.TelegramUser{TelegramID: 1001, Username: "ivan"}
	created, err := svc.CreateTaskFromText(ctx, user, "Нужно поливать петунии каждый день")
	if err != nil {
		t.Fatalf("CreateTaskFromText error: %v", err)
	}
	if created.Task.RecurrenceRule == nil || *created.Task.RecurrenceRule != domain.RecurrenceDaily {
		t.Fatalf("recurrence = %v, want daily", created.Task.RecurrenceRule)
	}
	if created.Task.Title != "Нужно поливать петунии" {
		t.Fatalf("title = %q", created.Task.Title)
	}
	assertServiceTimePtr(t, "initial remind", created.Task.RemindAt, ptrServiceTime(time.Date(2026, 6, 20, 10, 0, 0, 0, loc)))

	done, err := svc.MarkDone(ctx, user, created.Task.ID)
	if err != nil {
		t.Fatalf("MarkDone error: %v", err)
	}
	if done.Status == domain.StatusDone || done.DoneAt != nil {
		t.Fatalf("recurring task was closed: %+v", done)
	}
	assertServiceTimePtr(t, "next remind", done.RemindAt, ptrServiceTime(time.Date(2026, 6, 21, 10, 0, 0, 0, loc)))
	if len(store.reminders) != 2 {
		t.Fatalf("reminders count = %d, want 2", len(store.reminders))
	}
	if store.reminders[1].SentAt == nil {
		t.Fatal("initial recurring reminder was not marked sent")
	}
}

func TestLinkedProfileAssigneeResolution(t *testing.T) {
	ctx := context.Background()
	loc := mustLocation(t)
	store := newFakeStore()
	svc := New(store, "Europe/Moscow", loc)
	svc.now = func() time.Time {
		return time.Date(2026, 6, 19, 10, 0, 0, 0, loc)
	}

	ivan := domain.TelegramUser{TelegramID: 1001, Username: "ivan", FirstName: "Иван"}
	mom := domain.TelegramUser{TelegramID: 2002, Username: "mom", FirstName: "Таня"}

	invite, err := svc.CreateProfileLinkInvite(ctx, ivan, []string{"мама", "Таня"})
	if err != nil {
		t.Fatalf("CreateProfileLinkInvite error: %v", err)
	}
	if _, err := svc.AcceptProfileLinkInvite(ctx, mom, invite.Token, []string{"Ваня", "Иван", "сын"}); err != nil {
		t.Fatalf("AcceptProfileLinkInvite error: %v", err)
	}
	ivanUser := store.usersByTelegram[ivan.TelegramID]
	momUser := store.usersByTelegram[mom.TelegramID]

	explicit, err := svc.CreateTaskFromText(ctx, ivan, "поставь маме задачу завтра купить молоко")
	if err != nil {
		t.Fatalf("explicit assignee CreateTaskFromText error: %v", err)
	}
	assertAssignee(t, explicit.Task, momUser.ID)
	if explicit.Task.CreatorUserID != ivanUser.ID {
		t.Fatalf("creator = %d, want %d", explicit.Task.CreatorUserID, ivanUser.ID)
	}
	if explicit.Task.WorkspaceID != store.workspacesByUser[momUser.ID].ID {
		t.Fatalf("workspace = %d, want mom workspace %d", explicit.Task.WorkspaceID, store.workspacesByUser[momUser.ID].ID)
	}
	if explicit.Task.Title != "купить молоко" {
		t.Fatalf("explicit title = %q, want %q", explicit.Task.Title, "купить молоко")
	}

	gift, err := svc.CreateTaskFromText(ctx, ivan, "купить маме подарок на ДР")
	if err != nil {
		t.Fatalf("gift CreateTaskFromText error: %v", err)
	}
	assertAssignee(t, gift.Task, ivanUser.ID)
	if gift.Task.Title != "купить маме подарок на ДР" {
		t.Fatalf("gift title = %q", gift.Task.Title)
	}

	wake, err := svc.CreateTaskFromText(ctx, mom, "разбудить Ваню в 10 утра")
	if err != nil {
		t.Fatalf("wake CreateTaskFromText error: %v", err)
	}
	assertAssignee(t, wake.Task, momUser.ID)

	buyForIvan, err := svc.CreateTaskFromText(ctx, mom, "Ваня, купи на Ozon чай https://Ozon.ru/Product/ABC123")
	if err != nil {
		t.Fatalf("buy for Ivan CreateTaskFromText error: %v", err)
	}
	assertAssignee(t, buyForIvan.Task, ivanUser.ID)
	if buyForIvan.Task.CreatorUserID != momUser.ID {
		t.Fatalf("creator = %d, want %d", buyForIvan.Task.CreatorUserID, momUser.ID)
	}
	if buyForIvan.Task.WorkspaceID != store.workspacesByUser[ivanUser.ID].ID {
		t.Fatalf("workspace = %d, want ivan workspace %d", buyForIvan.Task.WorkspaceID, store.workspacesByUser[ivanUser.ID].ID)
	}
	wantTitle := "купи на Ozon чай https://Ozon.ru/Product/ABC123"
	if buyForIvan.Task.Title != wantTitle {
		t.Fatalf("buy for Ivan title = %q, want %q", buyForIvan.Task.Title, wantTitle)
	}

	_, err = svc.CreateTaskFromText(ctx, ivan, "маме документы завтра")
	var clarification *AssigneeClarificationError
	if !errors.As(err, &clarification) {
		t.Fatalf("ambiguous err = %v, want AssigneeClarificationError", err)
	}
	if clarification.TaskText != "маме документы завтра" {
		t.Fatalf("clarification task text = %q", clarification.TaskText)
	}
	if len(clarification.Options) != 2 {
		t.Fatalf("clarification options = %+v, want self and mom", clarification.Options)
	}

	manual, err := svc.CreateTaskForAssignee(ctx, ivan, clarification.TaskText, momUser.ID)
	if err != nil {
		t.Fatalf("CreateTaskForAssignee error: %v", err)
	}
	assertAssignee(t, manual.Task, momUser.ID)
}

type fakeStore struct {
	nextUserID       int64
	nextWorkspaceID  int64
	nextTaskID       int64
	nextReminderID   int64
	nextLinkID       int64
	usersByTelegram  map[int64]*domain.User
	workspacesByUser map[int64]*domain.Workspace
	linksByToken     map[string]*domain.ProfileLink
	profileAliases   []fakeProfileAlias
	tasks            map[int64]*domain.Task
	reminders        map[int64]*domain.TaskReminder
}

type fakeProfileAlias struct {
	LinkID       int64
	OwnerUserID  int64
	TargetUserID *int64
	Alias        string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nextUserID:       1,
		nextWorkspaceID:  1,
		nextTaskID:       1,
		nextReminderID:   1,
		nextLinkID:       1,
		usersByTelegram:  make(map[int64]*domain.User),
		workspacesByUser: make(map[int64]*domain.Workspace),
		linksByToken:     make(map[string]*domain.ProfileLink),
		tasks:            make(map[int64]*domain.Task),
		reminders:        make(map[int64]*domain.TaskReminder),
	}
}

func (s *fakeStore) EnsureUser(_ context.Context, tgUser domain.TelegramUser, defaultTimezone string) (*domain.User, error) {
	if user, ok := s.usersByTelegram[tgUser.TelegramID]; ok {
		user.Username = tgUser.Username
		user.FirstName = tgUser.FirstName
		user.LastName = tgUser.LastName
		return cloneUser(user), nil
	}
	user := &domain.User{
		ID:         s.nextUserID,
		TelegramID: tgUser.TelegramID,
		Username:   tgUser.Username,
		FirstName:  tgUser.FirstName,
		LastName:   tgUser.LastName,
		Timezone:   defaultTimezone,
	}
	s.nextUserID++
	s.usersByTelegram[tgUser.TelegramID] = user
	return cloneUser(user), nil
}

func (s *fakeStore) EnsurePersonalWorkspace(_ context.Context, userID int64) (*domain.Workspace, error) {
	if workspace, ok := s.workspacesByUser[userID]; ok {
		return cloneWorkspace(workspace), nil
	}
	workspace := &domain.Workspace{ID: s.nextWorkspaceID, Name: "Personal", OwnerUserID: userID}
	s.nextWorkspaceID++
	s.workspacesByUser[userID] = workspace
	return cloneWorkspace(workspace), nil
}

func (s *fakeStore) CreateProfileLinkInvite(_ context.Context, inviterUserID int64, token string, aliases []domain.ProfileLinkAliasInput) (*domain.ProfileLink, error) {
	link := &domain.ProfileLink{
		ID:            s.nextLinkID,
		InviteToken:   token,
		InviterUserID: inviterUserID,
		Status:        domain.ProfileLinkPending,
	}
	s.nextLinkID++
	s.linksByToken[token] = link
	for _, alias := range aliases {
		s.profileAliases = append(s.profileAliases, fakeProfileAlias{
			LinkID:      link.ID,
			OwnerUserID: inviterUserID,
			Alias:       alias.Alias,
		})
	}
	return cloneProfileLink(link), nil
}

func (s *fakeStore) AcceptProfileLinkInvite(_ context.Context, token string, inviteeUserID int64, aliases []domain.ProfileLinkAliasInput, acceptedAt time.Time) (*domain.ProfileLink, error) {
	link, ok := s.linksByToken[token]
	if !ok {
		return nil, ErrProfileLinkNotFound
	}
	if link.Status != domain.ProfileLinkPending {
		return nil, ErrProfileLinkNotPending
	}
	if link.InviterUserID == inviteeUserID {
		return nil, ErrProfileLinkSelf
	}
	link.InviteeUserID = &inviteeUserID
	link.Status = domain.ProfileLinkActive
	link.AcceptedAt = &acceptedAt
	for i := range s.profileAliases {
		alias := &s.profileAliases[i]
		if alias.LinkID == link.ID && alias.OwnerUserID == link.InviterUserID && alias.TargetUserID == nil {
			value := inviteeUserID
			alias.TargetUserID = &value
		}
	}
	inviterID := link.InviterUserID
	for _, alias := range aliases {
		s.profileAliases = append(s.profileAliases, fakeProfileAlias{
			LinkID:       link.ID,
			OwnerUserID:  inviteeUserID,
			TargetUserID: &inviterID,
			Alias:        alias.Alias,
		})
	}
	return cloneProfileLink(link), nil
}

func (s *fakeStore) LinkedProfiles(_ context.Context, ownerUserID int64) ([]domain.LinkedProfile, error) {
	result := make([]domain.LinkedProfile, 0)
	for _, link := range s.linksByToken {
		if link.Status != domain.ProfileLinkActive || link.InviteeUserID == nil {
			continue
		}
		var targetID int64
		switch ownerUserID {
		case link.InviterUserID:
			targetID = *link.InviteeUserID
		case *link.InviteeUserID:
			targetID = link.InviterUserID
		default:
			continue
		}
		target := s.userByID(targetID)
		if target == nil {
			continue
		}
		profile := domain.LinkedProfile{
			LinkID: link.ID,
			User:   *cloneUser(target),
		}
		for _, alias := range s.profileAliases {
			if alias.LinkID == link.ID && alias.OwnerUserID == ownerUserID && alias.TargetUserID != nil && *alias.TargetUserID == targetID {
				profile.Aliases = append(profile.Aliases, alias.Alias)
			}
		}
		result = append(result, profile)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LinkID < result[j].LinkID
	})
	return result, nil
}

func (s *fakeStore) CreateTask(_ context.Context, task *domain.Task) error {
	task.ID = s.nextTaskID
	s.nextTaskID++
	copyTask := cloneTask(task)
	s.tasks[task.ID] = copyTask
	return nil
}

func (s *fakeStore) CreateTaskReminder(_ context.Context, taskID int64, remindAt time.Time) error {
	reminder := &domain.TaskReminder{ID: s.nextReminderID, TaskID: taskID, RemindAt: remindAt}
	s.nextReminderID++
	s.reminders[reminder.ID] = reminder
	return nil
}

func (s *fakeStore) CreateTaskEvent(_ context.Context, _ int64, _ int64, _ string, _ any) error {
	return nil
}

func (s *fakeStore) TaskByID(_ context.Context, taskID int64) (*domain.Task, error) {
	return cloneTask(s.tasks[taskID]), nil
}

func (s *fakeStore) TaskRecipient(_ context.Context, taskID int64) (*domain.User, error) {
	task := s.tasks[taskID]
	if task.AssigneeUserID != nil {
		return cloneUser(s.userByID(*task.AssigneeUserID)), nil
	}
	return cloneUser(s.userByID(task.CreatorUserID)), nil
}

func (s *fakeStore) UpdateTaskStatus(_ context.Context, taskID int64, _ int64, status domain.Status, at time.Time) (*domain.Task, error) {
	task := s.tasks[taskID]
	task.Status = status
	task.UpdatedAt = at
	if status == domain.StatusDone {
		task.DoneAt = &at
	}
	if status == domain.StatusCancelled {
		task.CancelledAt = &at
	}
	return cloneTask(task), nil
}

func (s *fakeStore) UpdateTaskSchedule(_ context.Context, taskID int64, _ int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	task := s.tasks[taskID]
	task.DueAt = dueAt
	task.RemindAt = remindAt
	task.UpdatedAt = at
	return cloneTask(task), nil
}

func (s *fakeStore) PostponeTask(_ context.Context, taskID int64, _ int64, dueAt *time.Time, remindAt *time.Time, at time.Time) (*domain.Task, error) {
	task := s.tasks[taskID]
	task.Status = domain.StatusPostponed
	task.DueAt = dueAt
	task.RemindAt = remindAt
	task.PostponedCount++
	task.UpdatedAt = at
	return cloneTask(task), nil
}

func (s *fakeStore) TasksForRange(_ context.Context, userID int64, start time.Time, end time.Time) ([]domain.Task, error) {
	tasks := make([]domain.Task, 0)
	for _, task := range s.tasks {
		if task.DueAt == nil || task.DueAt.Before(start) || !task.DueAt.Before(end) {
			continue
		}
		if task.Status != domain.StatusNew && task.Status != domain.StatusPlanned && task.Status != domain.StatusPostponed {
			continue
		}
		if task.AssigneeUserID != nil && *task.AssigneeUserID != userID {
			continue
		}
		tasks = append(tasks, *cloneTask(task))
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return tasks, nil
}

func (s *fakeStore) DueReminderNotifications(_ context.Context, _ time.Time, _ int) ([]ReminderNotification, error) {
	return nil, nil
}

func (s *fakeStore) MarkReminderSent(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *fakeStore) MarkTaskRemindersSentBefore(_ context.Context, taskID int64, before time.Time, sentAt time.Time) error {
	for _, reminder := range s.reminders {
		if reminder.TaskID == taskID && reminder.SentAt == nil && reminder.RemindAt.Before(before) {
			value := sentAt
			reminder.SentAt = &value
		}
	}
	return nil
}

func (s *fakeStore) UsersForDigest(_ context.Context) ([]domain.User, error) {
	users := make([]domain.User, 0, len(s.usersByTelegram))
	for _, user := range s.usersByTelegram {
		users = append(users, *cloneUser(user))
	}
	return users, nil
}

func (s *fakeStore) HasDigestRun(_ context.Context, _ int64, _ time.Time) (bool, error) {
	return false, nil
}

func (s *fakeStore) MarkDigestRun(_ context.Context, _ int64, _ time.Time, _ time.Time) error {
	return nil
}

func (s *fakeStore) userByID(userID int64) *domain.User {
	for _, user := range s.usersByTelegram {
		if user.ID == userID {
			return user
		}
	}
	return nil
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	clone := *task
	if task.AssigneeUserID != nil {
		value := *task.AssigneeUserID
		clone.AssigneeUserID = &value
	}
	if task.Description != nil {
		value := *task.Description
		clone.Description = &value
	}
	if task.Category != nil {
		value := *task.Category
		clone.Category = &value
	}
	if task.RecurrenceRule != nil {
		value := *task.RecurrenceRule
		clone.RecurrenceRule = &value
	}
	if task.DueAt != nil {
		value := *task.DueAt
		clone.DueAt = &value
	}
	if task.RemindAt != nil {
		value := *task.RemindAt
		clone.RemindAt = &value
	}
	if task.DoneAt != nil {
		value := *task.DoneAt
		clone.DoneAt = &value
	}
	if task.CancelledAt != nil {
		value := *task.CancelledAt
		clone.CancelledAt = &value
	}
	return &clone
}

func cloneProfileLink(link *domain.ProfileLink) *domain.ProfileLink {
	if link == nil {
		return nil
	}
	clone := *link
	if link.InviteeUserID != nil {
		value := *link.InviteeUserID
		clone.InviteeUserID = &value
	}
	if link.AcceptedAt != nil {
		value := *link.AcceptedAt
		clone.AcceptedAt = &value
	}
	if link.RevokedAt != nil {
		value := *link.RevokedAt
		clone.RevokedAt = &value
	}
	return &clone
}

func ptrServiceTime(t time.Time) *time.Time {
	return &t
}

func assertServiceTimePtr(t *testing.T, name string, got *time.Time, want *time.Time) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %v, want nil", name, got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, *want)
	}
	if !got.Equal(*want) {
		t.Fatalf("%s = %v, want %v", name, *got, *want)
	}
}

func assertAssignee(t *testing.T, task domain.Task, want int64) {
	t.Helper()
	if task.AssigneeUserID == nil {
		t.Fatalf("assignee is nil, want %d", want)
	}
	if *task.AssigneeUserID != want {
		t.Fatalf("assignee = %d, want %d", *task.AssigneeUserID, want)
	}
}

func cloneUser(user *domain.User) *domain.User {
	if user == nil {
		return nil
	}
	clone := *user
	return &clone
}

func cloneWorkspace(workspace *domain.Workspace) *domain.Workspace {
	if workspace == nil {
		return nil
	}
	clone := *workspace
	return &clone
}

func mustLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
