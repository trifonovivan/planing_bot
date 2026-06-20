package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/service"
)

func TestInviteWithoutArgsUsesNextMessageAsAliases(t *testing.T) {
	ctx := context.Background()
	store := &telegramFakeStore{}
	loc := mustLocation(t)
	planner := service.New(store, "Europe/Moscow", loc)

	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			messages = append(messages, payload.Text)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"username":"planner_test_bot","first_name":"Planner"}}`))
		default:
			t.Fatalf("path = %s, want /sendMessage or /getMe", r.URL.Path)
		}
	}))
	defer server.Close()

	bot := New("token", planner)
	bot.baseURL = server.URL
	from := user{ID: 1001, Username: "ivan", FirstName: "Иван"}
	targetChat := chat{ID: 2002}

	if err := bot.handleMessage(ctx, message{From: from, Chat: targetChat, Text: "/invite"}); err != nil {
		t.Fatalf("handle /invite: %v", err)
	}
	if len(store.invites) != 0 {
		t.Fatalf("invites = %d, want 0 before aliases", len(store.invites))
	}

	if err := bot.handleMessage(ctx, message{From: from, Chat: targetChat, Text: "мама, маме, Таня, мам"}); err != nil {
		t.Fatalf("handle aliases: %v", err)
	}
	if len(store.invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(store.invites))
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if !strings.Contains(messages[0], "Напиши алиасы") {
		t.Fatalf("first message = %q", messages[0])
	}
	if !strings.Contains(messages[1], "Инвайт создан") {
		t.Fatalf("second message = %q", messages[1])
	}
	if !strings.Contains(messages[1], "https://t.me/planner_test_bot?start=link_") {
		t.Fatalf("second message has no deep link: %q", messages[1])
	}
	if strings.Contains(messages[1], "Ссылка/код") {
		t.Fatalf("second message contains old link label: %q", messages[1])
	}
	if strings.Contains(messages[1], "Задача создана") {
		t.Fatalf("second message created a task: %q", messages[1])
	}
}

func TestFormatInviteFallbackUsesManualAcceptCode(t *testing.T) {
	result := &service.ProfileLinkInviteResult{
		Token:   "abc",
		Aliases: []string{"мама", "маме"},
	}

	got := formatInvite(result, "")
	if strings.Contains(got, "Ссылка/код") {
		t.Fatalf("message contains old label: %q", got)
	}
	if !strings.Contains(got, "Код для ручного принятия: link_abc") {
		t.Fatalf("message = %q, want manual code", got)
	}
	if !strings.Contains(got, "/accept link_abc") {
		t.Fatalf("message = %q, want accept command", got)
	}
}

func TestInviteStillRepliesWhenGetMeFails(t *testing.T) {
	ctx := context.Background()
	store := &telegramFakeStore{}
	loc := mustLocation(t)
	planner := service.New(store, "Europe/Moscow", loc)

	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getMe":
			http.Error(w, "temporary telegram error", http.StatusBadGateway)
		case "/sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			messages = append(messages, payload.Text)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("path = %s, want /sendMessage or /getMe", r.URL.Path)
		}
	}))
	defer server.Close()

	bot := New("token", planner)
	bot.baseURL = server.URL
	from := user{ID: 1001, Username: "ivan", FirstName: "Иван"}
	targetChat := chat{ID: 2002}

	if err := bot.handleMessage(ctx, message{From: from, Chat: targetChat, Text: "/link мама, мам, Таня"}); err != nil {
		t.Fatalf("handle /link: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0], "Код для ручного принятия: link_") {
		t.Fatalf("message = %q, want manual code fallback", messages[0])
	}
}

func TestInviteAliasInUseRepliesWithMessage(t *testing.T) {
	ctx := context.Background()
	store := &telegramFakeStore{createInviteErr: service.ErrProfileAliasInUse}
	loc := mustLocation(t)
	planner := service.New(store, "Europe/Moscow", loc)

	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendMessage" {
			t.Fatalf("path = %s, want /sendMessage", r.URL.Path)
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		messages = append(messages, payload.Text)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	bot := New("token", planner, WithBotUsername("iatrifonov_planing_bot"))
	bot.baseURL = server.URL
	from := user{ID: 1001, Username: "ivan", FirstName: "Иван"}
	targetChat := chat{ID: 2002}

	if err := bot.handleMessage(ctx, message{From: from, Chat: targetChat, Text: "/link мама, мам, Таня"}); err != nil {
		t.Fatalf("handle /link: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0], "Такие алиасы уже используются") {
		t.Fatalf("message = %q, want alias-in-use text", messages[0])
	}
}

type telegramFakeStore struct {
	user            *domain.User
	space           *domain.Workspace
	invites         [][]domain.ProfileLinkAliasInput
	createInviteErr error
}

func (s *telegramFakeStore) EnsureUser(_ context.Context, tgUser domain.TelegramUser, defaultTimezone string) (*domain.User, error) {
	if s.user == nil {
		s.user = &domain.User{
			ID:         1,
			TelegramID: tgUser.TelegramID,
			Username:   tgUser.Username,
			FirstName:  tgUser.FirstName,
			LastName:   tgUser.LastName,
			Timezone:   defaultTimezone,
		}
	}
	return s.user, nil
}

func (s *telegramFakeStore) EnsurePersonalWorkspace(_ context.Context, userID int64) (*domain.Workspace, error) {
	if s.space == nil {
		s.space = &domain.Workspace{ID: 1, OwnerUserID: userID, Name: "Personal"}
	}
	return s.space, nil
}

func (s *telegramFakeStore) CreateProfileLinkInvite(_ context.Context, inviterUserID int64, token string, aliases []domain.ProfileLinkAliasInput) (*domain.ProfileLink, error) {
	if s.createInviteErr != nil {
		return nil, s.createInviteErr
	}
	s.invites = append(s.invites, aliases)
	return &domain.ProfileLink{ID: int64(len(s.invites)), InviterUserID: inviterUserID, InviteToken: token, Status: domain.ProfileLinkPending}, nil
}

func (s *telegramFakeStore) AcceptProfileLinkInvite(context.Context, string, int64, []domain.ProfileLinkAliasInput, time.Time) (*domain.ProfileLink, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) LinkedProfiles(context.Context, int64) ([]domain.LinkedProfile, error) {
	return nil, nil
}

func (s *telegramFakeStore) CreateTask(context.Context, *domain.Task) error {
	return errors.New("task creation should not be called")
}

func (s *telegramFakeStore) CreateTaskReminder(context.Context, int64, time.Time) error {
	return nil
}

func (s *telegramFakeStore) CreateTaskEvent(context.Context, int64, int64, string, any) error {
	return nil
}

func (s *telegramFakeStore) TaskByID(context.Context, int64) (*domain.Task, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) TaskRecipient(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) UpdateTaskStatus(context.Context, int64, int64, domain.Status, time.Time) (*domain.Task, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) UpdateTaskSchedule(context.Context, int64, int64, *time.Time, *time.Time, time.Time) (*domain.Task, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) PostponeTask(context.Context, int64, int64, *time.Time, *time.Time, time.Time) (*domain.Task, error) {
	return nil, errors.New("not implemented")
}

func (s *telegramFakeStore) TasksForRange(context.Context, int64, time.Time, time.Time) ([]domain.Task, error) {
	return nil, nil
}

func (s *telegramFakeStore) DueReminderNotifications(context.Context, time.Time, int) ([]service.ReminderNotification, error) {
	return nil, nil
}

func (s *telegramFakeStore) MarkReminderSent(context.Context, int64, time.Time) error {
	return nil
}

func (s *telegramFakeStore) MarkTaskRemindersSentBefore(context.Context, int64, time.Time, time.Time) error {
	return nil
}

func (s *telegramFakeStore) UsersForDigest(context.Context) ([]domain.User, error) {
	return nil, nil
}

func (s *telegramFakeStore) HasDigestRun(context.Context, int64, time.Time) (bool, error) {
	return false, nil
}

func (s *telegramFakeStore) MarkDigestRun(context.Context, int64, time.Time, time.Time) error {
	return nil
}

func mustLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
