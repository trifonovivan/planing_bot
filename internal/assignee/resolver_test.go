package assignee

import "testing"

func TestResolverCornerCases(t *testing.T) {
	resolver := NewResolver(1, []Candidate{
		{UserID: 2, Name: "Мама", Aliases: []string{"мама", "Таня"}},
		{UserID: 3, Name: "Иван", Aliases: []string{"Ваня", "Иван", "сын"}},
	})

	tests := []struct {
		name        string
		text        string
		wantID      int64
		wantText    string
		wantSrc     Source
		wantClarify bool
	}{
		{
			name:     "explicit linked assignee",
			text:     "поставь маме задачу завтра купить молоко",
			wantID:   2,
			wantText: "завтра купить молоко",
			wantSrc:  SourceExplicitOther,
		},
		{
			name:     "linked assignee with modal",
			text:     "маме нужно оплатить интернет",
			wantID:   2,
			wantText: "оплатить интернет",
			wantSrc:  SourceExplicitOther,
		},
		{
			name:     "linked assignee with let marker",
			text:     "пусть мама заберет заказ",
			wantID:   2,
			wantText: "заберет заказ",
			wantSrc:  SourceExplicitOther,
		},
		{
			name:     "gift recipient is not assignee",
			text:     "купить маме подарок на ДР",
			wantID:   1,
			wantText: "купить маме подарок на ДР",
			wantSrc:  SourceObjectRule,
		},
		{
			name:     "wake object is not assignee",
			text:     "разбудить Ваню в 10 утра",
			wantID:   1,
			wantText: "разбудить Ваню в 10 утра",
			wantSrc:  SourceObjectRule,
		},
		{
			name:     "call object is not assignee",
			text:     "напомни мне позвонить маме",
			wantID:   1,
			wantText: "позвонить маме",
			wantSrc:  SourceExplicitSelf,
		},
		{
			name:        "ambiguous alias asks",
			text:        "маме документы завтра",
			wantID:      1,
			wantText:    "маме документы завтра",
			wantSrc:     SourceClarification,
			wantClarify: true,
		},
		{
			name:     "no alias defaults to self",
			text:     "завтра купить хлеб",
			wantID:   1,
			wantText: "завтра купить хлеб",
			wantSrc:  SourceDefaultSelf,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolver.Resolve(tt.text)
			if got.AssigneeUserID != tt.wantID {
				t.Fatalf("assignee = %d, want %d", got.AssigneeUserID, tt.wantID)
			}
			if got.TaskText != tt.wantText {
				t.Fatalf("task text = %q, want %q", got.TaskText, tt.wantText)
			}
			if got.Source != tt.wantSrc {
				t.Fatalf("source = %s, want %s", got.Source, tt.wantSrc)
			}
			if got.NeedsClarification != tt.wantClarify {
				t.Fatalf("clarification = %v, want %v", got.NeedsClarification, tt.wantClarify)
			}
		})
	}
}

func TestNormalizeAliasesGeneratesRussianForms(t *testing.T) {
	got := NormalizeAliases([]string{"Ваня, Иван, сын"})
	for _, want := range []string{"ваня", "ване", "ваню", "иван", "ивану", "ивана", "сын", "сыну", "сына"} {
		if !hasAlias(got, want) {
			t.Fatalf("aliases = %+v, want %q", got, want)
		}
	}
}

func TestMatchOption(t *testing.T) {
	candidates := []Candidate{{UserID: 2, Name: "Мама", Aliases: []string{"мама", "Таня"}}}
	if got, ok := MatchOption("мне", 1, candidates); !ok || got != 1 {
		t.Fatalf("self option = %d/%v, want 1/true", got, ok)
	}
	if got, ok := MatchOption("маме", 1, candidates); !ok || got != 2 {
		t.Fatalf("linked option = %d/%v, want 2/true", got, ok)
	}
}

func hasAlias(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
