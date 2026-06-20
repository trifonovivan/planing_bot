package mlparser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/parser"
)

func TestClientParseSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/parse" {
			t.Fatalf("path = %s, want /parse", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output": {
				"title": "оплатить интернет",
				"due_at": "2026-06-20T16:30:00+03:00",
				"remind_at": "2026-06-20T15:30:00+03:00",
				"priority": "p1",
				"category": "finance",
				"assignee": "Иван Трифонов",
				"repeat": null,
				"status": "success",
				"clarification_reason": null
			},
			"confidence": 0.81,
			"source": "hybrid",
			"time_source": "date_word"
		}`))
	}))
	defer server.Close()

	loc := mustLocation(t)
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, loc)
	result, err := New(server.URL+"/parse").Parse(context.Background(), "седня к 16-30 оплатить инет", now, loc)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if result.Title != "оплатить интернет" {
		t.Fatalf("Title = %q", result.Title)
	}
	if result.Priority != domain.PriorityP1 {
		t.Fatalf("Priority = %s", result.Priority)
	}
	if result.Category == nil || *result.Category != "Финансы" {
		t.Fatalf("Category = %v, want Финансы", result.Category)
	}
	assertTime(t, result.DueAt, time.Date(2026, 6, 20, 16, 30, 0, 0, loc))
	assertTime(t, result.RemindAt, time.Date(2026, 6, 20, 15, 30, 0, 0, loc))
	if !containsWarning(result.Warnings, "ml_parser_used") {
		t.Fatalf("warnings = %#v, want ml_parser_used", result.Warnings)
	}
}

func TestClientPrefersRuleScheduleForDeterministicRelativeTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output": {
				"title": "написать леше",
				"due_at": "2026-06-20T02:51:00+03:00",
				"remind_at": "2026-06-20T01:51:00+03:00",
				"priority": "p1",
				"category": null,
				"assignee": null,
				"repeat": null,
				"status": "success",
				"clarification_reason": null
			},
			"confidence": 0.81
		}`))
	}))
	defer server.Close()

	loc := mustLocation(t)
	now := time.Date(2026, 6, 20, 2, 21, 0, 0, loc)
	result, err := New(server.URL).Parse(context.Background(), "Написать Леше через полчаса", now, loc)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	assertTime(t, result.DueAt, time.Date(2026, 6, 20, 2, 51, 0, 0, loc))
	assertTime(t, result.RemindAt, time.Date(2026, 6, 20, 2, 26, 0, 0, loc))
	if !containsWarning(result.Warnings, "rule_parser_schedule_used") {
		t.Fatalf("warnings = %#v, want rule_parser_schedule_used", result.Warnings)
	}
}

func TestClientPrefersRuleScheduleForEndOfMonth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output": {
				"title": "оплатить интернет",
				"due_at": "2026-07-01T01:45:00+03:00",
				"remind_at": "2026-07-01T00:45:00+03:00",
				"priority": "p2",
				"category": "finance",
				"assignee": null,
				"repeat": null,
				"status": "success",
				"clarification_reason": null
			},
			"confidence": 0.81
		}`))
	}))
	defer server.Close()

	loc := mustLocation(t)
	now := time.Date(2026, 6, 20, 2, 31, 0, 0, loc)
	result, err := New(server.URL).Parse(context.Background(), "В конце месяца оплатить инет", now, loc)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	assertTime(t, result.DueAt, time.Date(2026, 6, 30, 23, 59, 0, 0, loc))
	assertTime(t, result.RemindAt, time.Date(2026, 6, 30, 10, 0, 0, 0, loc))
	if !containsWarning(result.Warnings, "rule_parser_schedule_used") {
		t.Fatalf("warnings = %#v, want rule_parser_schedule_used", result.Warnings)
	}
}

func TestClientFallsBackToRuleParserOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	loc := mustLocation(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, loc)
	result, err := New(server.URL).Parse(context.Background(), "завтра купить молоко", now, loc)
	if err != nil {
		t.Fatalf("Parse fallback error: %v", err)
	}
	if result.Title != "купить молоко" {
		t.Fatalf("Title = %q", result.Title)
	}
	if result.DueAt == nil {
		t.Fatal("DueAt is nil, want fallback parser due date")
	}
	if !containsWarning(result.Warnings, "rule_parser_fallback") {
		t.Fatalf("warnings = %#v, want fallback marker", result.Warnings)
	}
}

func TestClientRejectedMessageReturnsEmptyTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output": {
				"title": null,
				"due_at": null,
				"remind_at": null,
				"priority": null,
				"category": null,
				"assignee": null,
				"repeat": null,
				"status": "ignored",
				"clarification_reason": null
			},
			"confidence": 0.9
		}`))
	}))
	defer server.Close()

	loc := mustLocation(t)
	_, err := New(server.URL).Parse(context.Background(), "привет", time.Date(2026, 6, 19, 10, 0, 0, 0, loc), loc)
	if !errors.Is(err, parser.ErrEmptyTitle) {
		t.Fatalf("Parse error = %v, want ErrEmptyTitle", err)
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

func assertTime(t *testing.T, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil {
		t.Fatalf("time is nil, want %v", want)
	}
	if !got.Equal(want) {
		t.Fatalf("time = %v, want %v", got, want)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want || strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
