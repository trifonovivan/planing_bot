package assignee

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Source string

const (
	SourceDefaultSelf   Source = "default_self"
	SourceExplicitSelf  Source = "explicit_self"
	SourceExplicitOther Source = "explicit_other"
	SourceObjectRule    Source = "object_rule"
	SourceClarification Source = "clarification"
)

type Candidate struct {
	UserID  int64
	Name    string
	Aliases []string
}

type Option struct {
	UserID  int64
	Label   string
	Aliases []string
	Self    bool
}

type Resolution struct {
	AssigneeUserID     int64
	TaskText           string
	Source             Source
	MatchedAlias       string
	NeedsClarification bool
	Options            []Option
}

type Resolver struct {
	selfUserID int64
	candidates []preparedCandidate
}

type preparedCandidate struct {
	Candidate
	aliases []string
	pattern string
}

var selfAliases = []string{"я", "мне", "меня", "мной", "себе", "себя", "для меня"}

func NewResolver(selfUserID int64, candidates []Candidate) Resolver {
	prepared := make([]preparedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		aliases := NormalizeAliases(candidate.Aliases)
		if len(aliases) == 0 && candidate.Name != "" {
			aliases = NormalizeAliases([]string{candidate.Name})
		}
		if len(aliases) == 0 {
			continue
		}
		sort.SliceStable(aliases, func(i, j int) bool {
			return len([]rune(aliases[i])) > len([]rune(aliases[j]))
		})
		prepared = append(prepared, preparedCandidate{
			Candidate: candidate,
			aliases:   aliases,
			pattern:   aliasesPattern(aliases),
		})
	}
	return Resolver{selfUserID: selfUserID, candidates: prepared}
}

func (r Resolver) Resolve(text string) Resolution {
	taskText := cleanTaskText(text)
	if len(r.candidates) == 0 {
		return Resolution{AssigneeUserID: r.selfUserID, TaskText: taskText, Source: SourceDefaultSelf}
	}

	normalized := NormalizeText(text)
	if normalized == "" {
		return Resolution{AssigneeUserID: r.selfUserID, TaskText: taskText, Source: SourceDefaultSelf}
	}

	if stripped, ok := stripExplicitSelf(text); ok {
		return Resolution{AssigneeUserID: r.selfUserID, TaskText: cleanTaskText(stripped), Source: SourceExplicitSelf}
	}

	for _, candidate := range r.candidates {
		if stripped, alias, ok := stripExplicitOther(text, candidate.pattern); ok {
			return Resolution{
				AssigneeUserID: candidate.UserID,
				TaskText:       cleanTaskText(stripped),
				Source:         SourceExplicitOther,
				MatchedAlias:   alias,
			}
		}
	}

	for _, candidate := range r.candidates {
		if alias, ok := matchObjectRule(normalized, candidate.pattern); ok {
			return Resolution{
				AssigneeUserID: r.selfUserID,
				TaskText:       taskText,
				Source:         SourceObjectRule,
				MatchedAlias:   alias,
			}
		}
	}

	matched := make([]preparedCandidate, 0)
	for _, candidate := range r.candidates {
		if aliasRE(candidate.pattern).MatchString(normalized) {
			matched = append(matched, candidate)
		}
	}
	if len(matched) > 0 {
		return Resolution{
			AssigneeUserID:     r.selfUserID,
			TaskText:           taskText,
			Source:             SourceClarification,
			NeedsClarification: true,
			Options:            clarificationOptions(r.selfUserID, matched),
		}
	}

	return Resolution{AssigneeUserID: r.selfUserID, TaskText: taskText, Source: SourceDefaultSelf}
}

func MatchOption(text string, selfUserID int64, candidates []Candidate) (int64, bool) {
	normalized := NormalizeText(text)
	if normalized == "" {
		return 0, false
	}
	if containsAlias(normalized, aliasesPattern(selfAliases)) {
		return selfUserID, true
	}
	for _, candidate := range candidates {
		aliases := NormalizeAliases(candidate.Aliases)
		if len(aliases) == 0 && candidate.Name != "" {
			aliases = NormalizeAliases([]string{candidate.Name})
		}
		if len(aliases) == 0 {
			continue
		}
		if containsAlias(normalized, aliasesPattern(aliases)) {
			return candidate.UserID, true
		}
	}
	return 0, false
}

func NormalizeAliases(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, value := range values {
		for _, alias := range strings.Split(value, ",") {
			alias = NormalizeText(alias)
			if alias == "" {
				continue
			}
			for _, form := range expandAlias(alias) {
				if _, ok := seen[form]; ok || form == "" {
					continue
				}
				seen[form] = struct{}{}
				result = append(result, form)
			}
		}
	}
	sort.Strings(result)
	return result
}

func NormalizeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "ё", "е")
	text = regexp.MustCompile(`[\t\r\n]+`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.Trim(text, " \t\r\n,.!?:;")
	return text
}

func aliasesPattern(aliases []string) string {
	parts := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		parts = append(parts, regexp.QuoteMeta(alias))
	}
	return strings.Join(parts, "|")
}

func stripExplicitSelf(text string) (string, bool) {
	patterns := []string{
		`(?i)^\s*(?:напомни|поставь|создай|добавь|запиши)\s+(?:мне|себе)(?:\s+задачу)?\s+(.+)$`,
		`(?i)^\s*(?:мне|себе|для меня)\s+(?:надо|нужно|пора|следует)\s+(.+)$`,
		`(?i)^\s*(?:надо|нужно)\s+(?:мне|себе)\s+(.+)$`,
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if len(match) > 0 {
			return match[len(match)-1], true
		}
	}
	return "", false
}

func stripExplicitOther(text string, aliasPattern string) (string, string, bool) {
	patterns := []string{
		`(?i)^\s*(?:поставь|создай|добавь|запиши)\s+(?:задачу\s+)?(` + aliasPattern + `)(?:\s+задачу)?\s+(.+)$`,
		`(?i)^\s*(?:поставь|создай|добавь|запиши)\s+задачу\s+(` + aliasPattern + `)\s+(.+)$`,
		`(?i)^\s*(?:задача\s+)?для\s+(` + aliasPattern + `)\s*[:,-]?\s+(.+)$`,
		`(?i)^\s*(` + aliasPattern + `)\s*(?:[,!:-]\s*)?(?:надо|нужно|пора|следует)\s+(.+)$`,
		`(?i)^\s*(` + aliasPattern + `)\s*(?:[,!:-]\s*)?(?:должен|должна|должны)\s+(.+)$`,
		`(?i)^\s*пусть\s+(` + aliasPattern + `)\s+(.+)$`,
		`(?i)^\s*(` + aliasPattern + `)\s*(?:[,!:-]\s*)?((?:` + imperativeVerbPattern() + `)(?:\s+.+)?)$`,
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if len(match) == 3 {
			return match[2], match[1], true
		}
	}
	return "", "", false
}

func matchObjectRule(text string, aliasPattern string) (string, bool) {
	verbPattern := objectVerbPattern()
	re := regexp.MustCompile(`(?:^|[^\pL\pN_])(?:` + verbPattern + `)(?:\s+[^\s,.!?:;]+){0,4}\s+(` + aliasPattern + `)(?:$|[^\pL\pN_])`)
	match := re.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1], true
	}
	return "", false
}

func objectVerbPattern() string {
	verbs := []string{
		"купить", "купи", "заказать", "закажи", "подарить", "подари", "позвонить", "позвони",
		"написать", "напиши", "сказать", "скажи", "сообщить", "сообщи", "ответить", "ответь",
		"передать", "передай", "скинуть", "скинь", "отправить", "отправь", "разбудить", "разбуди",
		"встретить", "встреть", "забрать", "забери", "отвезти", "отвези", "поздравить", "поздравь",
		"помочь", "помоги", "привезти", "привези",
	}
	return strings.Join(verbs, "|")
}

func imperativeVerbPattern() string {
	verbs := []string{
		"купи", "закажи", "оплати", "забери", "привези", "отвези", "позвони", "напиши",
		"скажи", "сообщи", "ответь", "передай", "скинь", "отправь", "разбуди", "встреть",
		"поздравь", "помоги", "сделай", "проверь", "запиши", "найди", "посмотри", "зайди",
		"сходи", "возьми", "подготовь",
	}
	return strings.Join(verbs, "|")
}

func containsAlias(text string, aliasPattern string) bool {
	return aliasRE(aliasPattern).MatchString(text)
}

func aliasRE(aliasPattern string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\pL\pN_])(?:` + aliasPattern + `)(?:$|[^\pL\pN_])`)
}

func clarificationOptions(selfUserID int64, candidates []preparedCandidate) []Option {
	options := []Option{{
		UserID:  selfUserID,
		Label:   "мне",
		Aliases: selfAliases,
		Self:    true,
	}}
	for _, candidate := range candidates {
		label := candidate.Name
		if label == "" && len(candidate.Aliases) > 0 {
			label = candidate.Aliases[0]
		}
		options = append(options, Option{
			UserID:  candidate.UserID,
			Label:   label,
			Aliases: candidate.aliases,
		})
	}
	return options
}

func cleanTaskText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, " \t\r\n,.!?:;")
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return text
}

func expandAlias(alias string) []string {
	result := []string{alias}
	if strings.Contains(alias, " ") {
		return result
	}
	result = append(result, russianForms(alias)...)
	return result
}

func russianForms(alias string) []string {
	last, size := utf8.DecodeLastRuneInString(alias)
	if last == utf8.RuneError {
		return nil
	}
	stem := alias[:len(alias)-size]
	switch last {
	case 'а':
		if strings.HasSuffix(alias, "ша") || strings.HasSuffix(alias, "жа") || strings.HasSuffix(alias, "ча") || strings.HasSuffix(alias, "ща") {
			return []string{stem + "и", stem + "е", stem + "у", stem + "ей", stem + "ою"}
		}
		return []string{stem + "ы", stem + "е", stem + "у", stem + "ой", stem + "ою"}
	case 'я':
		return []string{stem + "и", stem + "е", stem + "ю", stem + "ей", stem + "ею"}
	case 'й':
		return []string{stem + "я", stem + "ю", stem + "ем", stem + "е"}
	case 'ь':
		return []string{stem + "я", stem + "ю", stem + "ем", stem + "е"}
	default:
		if unicode.IsLetter(last) {
			return []string{alias + "а", alias + "у", alias + "ом", alias + "е"}
		}
	}
	return nil
}
