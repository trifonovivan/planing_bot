package parser

import (
	"errors"
	"strings"
	"testing"
	"time"

	"planing_bot/internal/domain"
)

func TestParseRequiredScenarios(t *testing.T) {
	loc := mustLocation(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, loc) // Friday.

	tests := []struct {
		name       string
		text       string
		wantTitle  string
		wantDue    *time.Time
		wantRemind *time.Time
		wantPrio   domain.Priority
		wantCat    string
		wantWarn   string
		wantErr    error
	}{
		{
			name:       "tomorrow",
			text:       "завтра купить корм",
			wantTitle:  "купить корм",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP2,
			wantCat:    "Покупки",
		},
		{
			name:       "today with time",
			text:       "сегодня в 18:00 полить огурцы",
			wantTitle:  "полить огурцы",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 18, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 17, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Дача",
		},
		{
			name:       "in three days",
			text:       "через 3 дня оплатить интернет",
			wantTitle:  "оплатить интернет",
			wantDue:    ptrTime(time.Date(2026, 6, 22, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 22, 22, 59, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Финансы",
		},
		{
			name:       "in two hours",
			text:       "через 2 часа проверить задачу",
			wantTitle:  "проверить задачу",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 12, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 11, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
		},
		{
			name:       "month name",
			text:       "25 июня в 12:00 врач",
			wantTitle:  "врач",
			wantDue:    ptrTime(time.Date(2026, 6, 25, 12, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 25, 11, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Здоровье",
		},
		{
			name:       "dot date",
			text:       "25.06.2026 в 09:30 созвон",
			wantTitle:  "созвон",
			wantDue:    ptrTime(time.Date(2026, 6, 25, 9, 30, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 25, 8, 30, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
		},
		{
			name:       "iso date bare time",
			text:       "2026-06-25 09:30 встреча",
			wantTitle:  "встреча",
			wantDue:    ptrTime(time.Date(2026, 6, 25, 9, 30, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 25, 8, 30, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
		},
		{
			name:       "weekday part of day",
			text:       "в пятницу вечером купить продукты",
			wantTitle:  "купить продукты",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 19, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 18, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Покупки",
		},
		{
			name:       "weekend",
			text:       "на выходных полить теплицу",
			wantTitle:  "полить теплицу",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Дача",
		},
		{
			name:      "urgent",
			text:      "срочно оплатить ипотеку",
			wantTitle: "оплатить ипотеку",
			wantPrio:  domain.PriorityP1,
			wantCat:   "Финансы",
		},
		{
			name:      "not urgent beats urgent",
			text:      "не срочно посмотреть PostgreSQL internals",
			wantTitle: "посмотреть PostgreSQL internals",
			wantPrio:  domain.PriorityP4,
			wantCat:   "Работа",
		},
		{
			name:      "someday",
			text:      "когда-нибудь изучить ClickHouse",
			wantTitle: "изучить ClickHouse",
			wantPrio:  domain.PriorityP4,
		},
		{
			name:       "duplicate date",
			text:       "завтра завтра купить корм",
			wantTitle:  "купить корм",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP2,
			wantCat:    "Покупки",
			wantWarn:   "matched multiple date expressions",
		},
		{
			name:       "time passed goes tomorrow",
			text:       "в 9 созвон",
			wantTitle:  "созвон",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 8, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
		},
		{
			name:       "time later today",
			text:       "в 18 созвон",
			wantTitle:  "созвон",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 18, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 17, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
		},
		{
			name:       "after midnight",
			text:       "завтра в 00:30 проверить сервер",
			wantTitle:  "проверить сервер",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 0, 30, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 23, 30, 0, 0, loc)),
			wantPrio:   domain.PriorityP2,
		},
		{
			name:       "zero days",
			text:       "через 0 дней сделать задачу",
			wantTitle:  "сделать задачу",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 22, 59, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Работа",
			wantWarn:   "zero relative duration",
		},
		{
			name:      "invalid date",
			text:      "25.13 купить корм",
			wantTitle: "купить корм",
			wantPrio:  domain.PriorityP3,
			wantCat:   "Покупки",
			wantWarn:  "invalid date",
		},
		{
			name:      "urgent and not urgent",
			text:      "срочно не срочно купить корм",
			wantTitle: "купить корм",
			wantPrio:  domain.PriorityP4,
			wantCat:   "Покупки",
		},
		{
			name:    "empty",
			text:    "",
			wantErr: ErrEmptyTitle,
		},
		{
			name:       "only date and time",
			text:       "завтра в 10",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 10, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP2,
			wantErr:    ErrEmptyTitle,
		},
		{
			name:       "spaces and commas",
			text:       "  ,  завтра,   купить    корм , ",
			wantTitle:  "купить корм",
			wantDue:    ptrTime(time.Date(2026, 6, 20, 23, 59, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 20, 9, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP2,
			wantCat:    "Покупки",
		},
		{
			name:      "multiple categories",
			text:      "купить страховка для машины",
			wantTitle: "купить страховка для машины",
			wantPrio:  domain.PriorityP3,
			wantCat:   "Финансы",
			wantWarn:  "matched multiple categories",
		},
		{
			name:       "day after tomorrow morning",
			text:       "послезавтра утром сдать анализы",
			wantTitle:  "сдать анализы",
			wantDue:    ptrTime(time.Date(2026, 6, 21, 9, 0, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 21, 8, 0, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
			wantCat:    "Здоровье",
		},
		{
			name:       "in fifteen minutes",
			text:       "через 15 минут проверить духовку",
			wantTitle:  "проверить духовку",
			wantDue:    ptrTime(time.Date(2026, 6, 19, 10, 15, 0, 0, loc)),
			wantRemind: ptrTime(time.Date(2026, 6, 19, 10, 5, 0, 0, loc)),
			wantPrio:   domain.PriorityP3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.text, now, loc)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && tt.wantTitle == "" {
				return
			}
			if got.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			assertTimePtr(t, "due", got.DueAt, tt.wantDue)
			assertTimePtr(t, "remind", got.RemindAt, tt.wantRemind)
			if got.Priority != tt.wantPrio {
				t.Fatalf("priority = %s, want %s", got.Priority, tt.wantPrio)
			}
			if tt.wantCat == "" && got.Category != nil {
				t.Fatalf("category = %q, want nil", *got.Category)
			}
			if tt.wantCat != "" {
				if got.Category == nil || *got.Category != tt.wantCat {
					t.Fatalf("category = %v, want %q", got.Category, tt.wantCat)
				}
			}
			if tt.wantWarn != "" && !hasWarning(got.Warnings, tt.wantWarn) {
				t.Fatalf("warnings = %v, want %q", got.Warnings, tt.wantWarn)
			}
		})
	}
}

func TestPriorityDetection(t *testing.T) {
	loc := mustLocation(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, loc)
	tests := map[string]domain.Priority{
		"горит отправить письмо":         domain.PriorityP1,
		"важно прочитать документ":       domain.PriorityP2,
		"потом разобрать заметки":        domain.PriorityP4,
		"срочно не срочно купить корм":   domain.PriorityP4,
		"обычная задача без маркеров":    domain.PriorityP3,
		"asap проверить прод":            domain.PriorityP1,
		"на неделе посмотреть материалы": domain.PriorityP2,
	}
	for text, want := range tests {
		got, err := Parse(text, now, loc)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", text, err)
		}
		if got.Priority != want {
			t.Fatalf("Parse(%q).Priority = %s, want %s", text, got.Priority, want)
		}
	}
}

func TestCategoryDetection(t *testing.T) {
	loc := mustLocation(t)
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, loc)
	tests := map[string]string{
		"созвон по работе":       "Работа",
		"подготовить диплом":     "Учеба",
		"оплатить налог":         "Финансы",
		"полить огурцы":          "Дача",
		"купить масло для lexus": "Авто",
		"заказать корм":          "Покупки",
		"купить таблетки":        "Покупки",
	}
	for text, want := range tests {
		got, err := Parse(text, now, loc)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", text, err)
		}
		if got.Category == nil || *got.Category != want {
			t.Fatalf("Parse(%q).Category = %v, want %q", text, got.Category, want)
		}
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

func ptrTime(t time.Time) *time.Time {
	return &t
}

func assertTimePtr(t *testing.T, name string, got *time.Time, want *time.Time) {
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

func hasWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
