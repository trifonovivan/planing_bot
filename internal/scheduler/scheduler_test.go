package scheduler

import (
	"strings"
	"testing"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/service"
)

func TestFormatReminderUsesUserLocation(t *testing.T) {
	loc := mustLocation(t)
	dueUTC := time.Date(2026, 6, 20, 6, 0, 0, 0, time.UTC)
	task := domain.Task{Title: "полить огурцы", DueAt: &dueUTC}

	got := formatReminder(task, loc)
	if !strings.Contains(got, "Срок задачи: 20.06.2026 09:00") {
		t.Fatalf("message = %q, want Moscow time", got)
	}
	if strings.Contains(got, "06:00") {
		t.Fatalf("message = %q, contains UTC time", got)
	}
}

func TestFormatDigestUsesUserLocation(t *testing.T) {
	dueUTC := time.Date(2026, 6, 20, 6, 0, 0, 0, time.UTC)
	digest := service.DigestNotification{
		User: domain.User{Timezone: "Europe/Moscow"},
		Tasks: []domain.Task{
			{Title: "полить огурцы", Priority: domain.PriorityP1, DueAt: &dueUTC},
		},
	}

	got := formatDigest(digest)
	if !strings.Contains(got, "- полить огурцы — 09:00") {
		t.Fatalf("digest = %q, want Moscow time", got)
	}
	if strings.Contains(got, "06:00") {
		t.Fatalf("digest = %q, contains UTC time", got)
	}
}

func mustLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
