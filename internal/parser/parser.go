package parser

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"planing_bot/internal/domain"
)

var ErrEmptyTitle = errors.New("empty task title")

type ParseResult struct {
	Title          string
	DueAt          *time.Time
	RemindAt       *time.Time
	Priority       domain.Priority
	Category       *string
	RecurrenceRule *domain.RecurrenceRule
	Confidence     float64
	Warnings       []string
}

type parsedDate struct {
	value time.Time
	found bool
}

type parsedClock struct {
	hour   int
	minute int
	found  bool
}

func Parse(text string, now time.Time, location *time.Location) (ParseResult, error) {
	if location == nil {
		location = time.Local
	}
	now = now.In(location)
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ParseResult{Priority: domain.PriorityP3, Confidence: 0}, ErrEmptyTitle
	}

	explicitReminder, reminderWarnings := parseExplicitReminder(strings.ToLower(raw), now, location)
	rawForTask := stripExplicitReminder(raw)
	lower := strings.ToLower(rawForTask)
	warnings := make([]string, 0)
	warnings = append(warnings, reminderWarnings...)
	priority := detectPriority(lower)
	category, categoryWarnings := detectCategory(lower)
	warnings = append(warnings, categoryWarnings...)
	recurrenceRule, recurrenceClock := detectRecurrence(lower)

	exactDue, durationWarnings := parseRelativeDuration(lower, now)
	warnings = append(warnings, durationWarnings...)

	date, dateWarnings := parseDate(lower, now)
	warnings = append(warnings, dateWarnings...)

	clock, clockWarnings := parseClock(lower)
	warnings = append(warnings, clockWarnings...)

	var dueAt *time.Time
	var remindAt *time.Time
	if exactDue != nil {
		due := exactDue.In(location)
		dueAt = &due
		remind := reminderBeforeDue(due, now)
		remindAt = &remind
	} else if date.found && clock.found {
		due := time.Date(date.value.Year(), date.value.Month(), date.value.Day(), clock.hour, clock.minute, 0, 0, location)
		dueAt = &due
		remind := reminderBeforeDue(due, now)
		remindAt = &remind
	} else if date.found {
		due := time.Date(date.value.Year(), date.value.Month(), date.value.Day(), 23, 59, 0, 0, location)
		dueAt = &due
		remind := time.Date(date.value.Year(), date.value.Month(), date.value.Day(), 10, 0, 0, 0, location)
		remindAt = &remind
	} else if clock.found {
		due := time.Date(now.Year(), now.Month(), now.Day(), clock.hour, clock.minute, 0, 0, location)
		if !due.After(now) {
			due = due.AddDate(0, 0, 1)
		}
		dueAt = &due
		remind := reminderBeforeDue(due, now)
		remindAt = &remind
	}
	if recurrenceRule != nil && dueAt == nil && remindAt == nil {
		remind := nextDailyReminder(now, location, recurrenceClock)
		due := time.Date(remind.Year(), remind.Month(), remind.Day(), 23, 59, 0, 0, location)
		dueAt = &due
		remindAt = &remind
	}
	if explicitReminder != nil {
		remindAt = explicitReminder
	}

	title := cleanTitle(rawForTask)
	if title == "" {
		return ParseResult{
			Priority:       priority,
			Category:       category,
			RecurrenceRule: recurrenceRule,
			DueAt:          dueAt,
			RemindAt:       remindAt,
			Confidence:     0.2,
			Warnings:       warnings,
		}, ErrEmptyTitle
	}

	confidence := 0.65
	if dueAt != nil || category != nil || priority != domain.PriorityP3 {
		confidence = 0.85
	}
	if recurrenceRule != nil {
		confidence = 0.9
	}
	if len(warnings) > 0 {
		confidence -= 0.1
	}

	return ParseResult{
		Title:          title,
		DueAt:          dueAt,
		RemindAt:       remindAt,
		Priority:       priority,
		Category:       category,
		RecurrenceRule: recurrenceRule,
		Confidence:     confidence,
		Warnings:       warnings,
	}, nil
}

func nextDailyReminder(now time.Time, location *time.Location, clock parsedClock) time.Time {
	hour, minute := 10, 0
	if clock.found {
		hour = clock.hour
		minute = clock.minute
	}
	remind := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, location)
	if !remind.After(now) {
		remind = remind.AddDate(0, 0, 1)
	}
	return remind
}

func reminderBeforeDue(due time.Time, now time.Time) time.Time {
	remind := due.Add(-time.Hour)
	if remind.Before(now) {
		return now.Add(5 * time.Minute)
	}
	return remind
}

func parseExplicitReminder(lower string, now time.Time, location *time.Location) (*time.Time, []string) {
	index := strings.Index(lower, "напомни")
	if index < 0 {
		return nil, nil
	}
	clause := lower[index:]
	clock, clockWarnings := parseClock(clause)
	warnings := append([]string{}, clockWarnings...)
	if !clock.found {
		return nil, append(warnings, "invalid reminder expression")
	}
	date, dateWarnings := parseDate(clause, now)
	warnings = append(warnings, dateWarnings...)
	if date.found {
		remind := time.Date(date.value.Year(), date.value.Month(), date.value.Day(), clock.hour, clock.minute, 0, 0, location)
		return &remind, warnings
	}
	remind := time.Date(now.Year(), now.Month(), now.Day(), clock.hour, clock.minute, 0, 0, location)
	if !remind.After(now) {
		remind = remind.AddDate(0, 0, 1)
	}
	return &remind, warnings
}

func stripExplicitReminder(raw string) string {
	lower := strings.ToLower(raw)
	index := strings.Index(lower, "напомни")
	if index < 0 {
		return raw
	}
	return strings.Trim(raw[:index], " \t\r\n,")
}

func parseRelativeDuration(lower string, now time.Time) (*time.Time, []string) {
	re := regexp.MustCompile(`(?i)(^|[\s,])через\s+(?:(\d+)\s+)?(минуту|минут[а-я]*|час[а-я]*|день|дня|дней|неделю|недел[а-я]*)($|[\s,])`)
	matches := re.FindAllStringSubmatch(lower, -1)
	warnings := make([]string, 0)
	if len(matches) == 0 {
		return nil, warnings
	}
	if len(matches) > 1 {
		warnings = append(warnings, "matched multiple date expressions")
	}
	n := 1
	if matches[0][2] != "" {
		parsed, err := strconv.Atoi(matches[0][2])
		if err != nil {
			return nil, append(warnings, "invalid relative duration")
		}
		n = parsed
	}
	if n == 0 {
		warnings = append(warnings, "zero relative duration")
	}

	unit := matches[0][3]
	var due time.Time
	switch {
	case strings.HasPrefix(unit, "минут"):
		due = now.Add(time.Duration(n) * time.Minute)
	case strings.HasPrefix(unit, "час"):
		due = now.Add(time.Duration(n) * time.Hour)
	case strings.HasPrefix(unit, "д"):
		due = now.AddDate(0, 0, n)
		due = time.Date(due.Year(), due.Month(), due.Day(), 23, 59, 0, 0, now.Location())
	case strings.HasPrefix(unit, "недел"):
		due = now.AddDate(0, 0, n*7)
		due = time.Date(due.Year(), due.Month(), due.Day(), 23, 59, 0, 0, now.Location())
	default:
		return nil, append(warnings, "unknown relative duration unit")
	}
	return &due, warnings
}

func parseDate(lower string, now time.Time) (parsedDate, []string) {
	warnings := make([]string, 0)
	expressionCount := countDateExpressions(lower)
	if expressionCount > 1 {
		warnings = append(warnings, "matched multiple date expressions")
	}

	if d, ok := parseRelativeDateWord(lower, now); ok {
		return parsedDate{value: d, found: true}, warnings
	}
	if d, ok := parseWeekday(lower, now); ok {
		return parsedDate{value: d, found: true}, warnings
	}
	if d, ok, warn := parseISODate(lower, now.Location()); ok || warn != "" {
		if warn != "" {
			warnings = append(warnings, warn)
		}
		return parsedDate{value: d, found: ok}, warnings
	}
	if d, ok, warn := parseDotDate(lower, now); ok || warn != "" {
		if warn != "" {
			warnings = append(warnings, warn)
		}
		return parsedDate{value: d, found: ok}, warnings
	}
	if d, ok := parseMonthNameDate(lower, now); ok {
		return parsedDate{value: d, found: true}, warnings
	}

	return parsedDate{}, warnings
}

func countDateExpressions(lower string) int {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(^|[\s,])через\s+(?:\d+\s+)?(минуту|минут[а-я]*|час[а-я]*|день|дня|дней|неделю|недел[а-я]*)($|[\s,])`),
		regexp.MustCompile(`(?i)(^|[\s,])(в|во)\s+(понедельник|вторник|среду|четверг|пятницу|субботу|воскресенье|воскресение)($|[\s,])`),
		regexp.MustCompile(`(?i)(^|[\s,])на\s+выходных($|[\s,])`),
		regexp.MustCompile(`\d{4}-\d{2}-\d{2}`),
		regexp.MustCompile(`\d{1,2}\.\d{1,2}(?:\.\d{4})?`),
		regexp.MustCompile(`(?i)(^|[\s,])\d{1,2}\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)($|[\s,])`),
	}
	total := countAnyToken(lower, "сегодня", "завтра", "послезавтра")
	for _, pattern := range patterns {
		total += len(pattern.FindAllString(lower, -1))
	}
	return total
}

func parseRelativeDateWord(lower string, now time.Time) (time.Time, bool) {
	switch {
	case containsToken(lower, "послезавтра"):
		return dateOnly(now.AddDate(0, 0, 2)), true
	case containsToken(lower, "завтра"):
		return dateOnly(now.AddDate(0, 0, 1)), true
	case containsToken(lower, "сегодня"):
		return dateOnly(now), true
	default:
		return time.Time{}, false
	}
}

func parseWeekday(lower string, now time.Time) (time.Time, bool) {
	if regexp.MustCompile(`(?i)(^|[\s,])на\s+выходных($|[\s,])`).MatchString(lower) {
		return nextWeekday(now, time.Saturday), true
	}

	re := regexp.MustCompile(`(?i)(^|[\s,])(в|во)\s+(понедельник|вторник|среду|четверг|пятницу|субботу|воскресенье|воскресение)($|[\s,])`)
	match := re.FindStringSubmatch(lower)
	if len(match) == 0 {
		return time.Time{}, false
	}

	targets := map[string]time.Weekday{
		"понедельник": time.Monday,
		"вторник":     time.Tuesday,
		"среду":       time.Wednesday,
		"четверг":     time.Thursday,
		"пятницу":     time.Friday,
		"субботу":     time.Saturday,
		"воскресенье": time.Sunday,
		"воскресение": time.Sunday,
	}
	target, ok := targets[match[3]]
	if !ok {
		return time.Time{}, false
	}
	return nextWeekday(now, target), true
}

func parseISODate(lower string, location *time.Location) (time.Time, bool, string) {
	re := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	value := re.FindString(lower)
	if value == "" {
		return time.Time{}, false, ""
	}
	d, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, false, "invalid date"
	}
	return d, true, ""
}

func parseDotDate(lower string, now time.Time) (time.Time, bool, string) {
	re := regexp.MustCompile(`(^|[\s,])(\d{1,2})\.(\d{1,2})(?:\.(\d{4}))?($|[\s,])`)
	match := re.FindStringSubmatch(lower)
	if len(match) == 0 {
		return time.Time{}, false, ""
	}
	day, _ := strconv.Atoi(match[2])
	month, _ := strconv.Atoi(match[3])
	year := now.Year()
	if match[4] != "" {
		year, _ = strconv.Atoi(match[4])
	}
	if month < 1 || month > 12 || day < 1 || day > daysInMonth(year, time.Month(month)) {
		return time.Time{}, false, "invalid date"
	}
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, now.Location())
	if match[4] == "" && d.Before(dateOnly(now)) {
		d = d.AddDate(1, 0, 0)
	}
	return d, true, ""
}

func parseMonthNameDate(lower string, now time.Time) (time.Time, bool) {
	re := regexp.MustCompile(`(?i)(^|[\s,])(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)($|[\s,])`)
	match := re.FindStringSubmatch(lower)
	if len(match) == 0 {
		return time.Time{}, false
	}
	day, _ := strconv.Atoi(match[2])
	month, ok := monthByName(match[3])
	if !ok || day < 1 || day > daysInMonth(now.Year(), month) {
		return time.Time{}, false
	}
	d := time.Date(now.Year(), month, day, 0, 0, 0, 0, now.Location())
	if d.Before(dateOnly(now)) {
		d = d.AddDate(1, 0, 0)
	}
	return d, true
}

func parseClock(lower string) (parsedClock, []string) {
	warnings := make([]string, 0)
	matches := make([]parsedClock, 0)

	preposition := regexp.MustCompile(`(?i)(^|[\s,])(в|к|до)\s+(\d{1,2})(?::(\d{2}))?($|[\s,])`)
	for _, match := range preposition.FindAllStringSubmatch(lower, -1) {
		clock, ok := clockFromParts(match[3], match[4])
		if ok {
			matches = append(matches, clock)
		}
	}

	if len(matches) == 0 {
		bare := regexp.MustCompile(`(^|[\s,])(\d{1,2}):(\d{2})($|[\s,])`)
		for _, match := range bare.FindAllStringSubmatch(lower, -1) {
			clock, ok := clockFromParts(match[2], match[3])
			if ok {
				matches = append(matches, clock)
			}
		}
	}

	partOfDay := []struct {
		word   string
		hour   int
		minute int
	}{
		{word: "утром", hour: 10},
		{word: "днём", hour: 12},
		{word: "днем", hour: 12},
		{word: "вечером", hour: 20},
		{word: "ночью", hour: 22},
	}
	for _, item := range partOfDay {
		if containsToken(lower, item.word) {
			matches = append(matches, parsedClock{hour: item.hour, minute: item.minute, found: true})
		}
	}

	if len(matches) == 0 {
		return parsedClock{}, warnings
	}
	if len(matches) > 1 {
		warnings = append(warnings, "matched multiple time expressions")
	}
	return matches[0], warnings
}

func clockFromParts(hourPart string, minutePart string) (parsedClock, bool) {
	hour, err := strconv.Atoi(hourPart)
	if err != nil {
		return parsedClock{}, false
	}
	minute := 0
	if minutePart != "" {
		minute, err = strconv.Atoi(minutePart)
		if err != nil {
			return parsedClock{}, false
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return parsedClock{}, false
	}
	return parsedClock{hour: hour, minute: minute, found: true}, true
}

func detectPriority(lower string) domain.Priority {
	if strings.Contains(lower, "не срочно") ||
		strings.Contains(lower, "когда-нибудь") ||
		strings.Contains(lower, "потом") ||
		strings.Contains(lower, "идея") ||
		strings.Contains(lower, "someday") {
		return domain.PriorityP4
	}

	p1 := []string{"очень срочно", "срочно", "обязательно", "сегодня обязательно", "asap", "горит"}
	for _, marker := range p1 {
		if strings.Contains(lower, marker) {
			return domain.PriorityP1
		}
	}

	p2 := []string{"важно", "желательно", "на неделе"}
	for _, marker := range p2 {
		if strings.Contains(lower, marker) {
			return domain.PriorityP2
		}
	}
	if containsToken(lower, "завтра") {
		return domain.PriorityP2
	}

	return domain.PriorityP3
}

func detectCategory(lower string) (*string, []string) {
	rules := []struct {
		name     string
		keywords []string
	}{
		{name: "Работа", keywords: []string{"работ", "тдр", "kong", "postgres", "код", "задач", "созвон", "встреча"}},
		{name: "Учеба", keywords: []string{"учеба", "диплом", "экзамен", "институт"}},
		{name: "Финансы", keywords: []string{"ипотека", "вклад", "инвестиции", "налог", "страховка", "оплатить"}},
		{name: "Дача", keywords: []string{"огурцы", "томаты", "смородина", "теплица", "грядки", "удобрения", "полить", "петуни"}},
		{name: "Авто", keywords: []string{"машина", "lexus", "шины", "масло", "страховка", "бензин"}},
		{name: "Покупки", keywords: []string{"купить", "заказать", "маркет", "озон", "wildberries"}},
		{name: "Здоровье", keywords: []string{"врач", "давление", "анализы", "таблетки", "аптека"}},
	}

	matched := make([]string, 0)
	for _, rule := range rules {
		for _, keyword := range rule.keywords {
			if strings.Contains(lower, keyword) {
				matched = append(matched, rule.name)
				break
			}
		}
	}
	if len(matched) == 0 {
		return nil, nil
	}
	warnings := make([]string, 0)
	if len(matched) > 1 {
		warnings = append(warnings, "matched multiple categories")
	}
	return &matched[0], warnings
}

func detectRecurrence(lower string) (*domain.RecurrenceRule, parsedClock) {
	rule := domain.RecurrenceDaily
	switch {
	case regexp.MustCompile(`(?i)(^|[\s,])(каждый\s+день|ежедневно)($|[\s,])`).MatchString(lower):
		return &rule, parsedClock{hour: 10, minute: 0, found: true}
	case regexp.MustCompile(`(?i)(^|[\s,])каждое\s+утро($|[\s,])`).MatchString(lower):
		return &rule, parsedClock{hour: 10, minute: 0, found: true}
	case regexp.MustCompile(`(?i)(^|[\s,])каждый\s+вечер($|[\s,])`).MatchString(lower):
		return &rule, parsedClock{hour: 20, minute: 0, found: true}
	default:
		return nil, parsedClock{}
	}
}

func cleanTitle(text string) string {
	title := text
	patterns := []string{
		`(?i)(^|[\s,])через\s+(?:\d+\s+)?(минуту|минут[а-я]*|час[а-я]*|день|дня|дней|неделю|недел[а-я]*)($|[\s,])`,
		`(?i)(^|[\s,])(послезавтра|сегодня|завтра)($|[\s,])`,
		`(?i)(^|[\s,])(в|во)\s+(понедельник|вторник|среду|четверг|пятницу|субботу|воскресенье|воскресение)($|[\s,])`,
		`(?i)(^|[\s,])на\s+выходных($|[\s,])`,
		`\d{4}-\d{2}-\d{2}`,
		`(^|[\s,])\d{1,2}\.\d{1,2}(?:\.\d{4})?($|[\s,])`,
		`(?i)(^|[\s,])\d{1,2}\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)($|[\s,])`,
		`(?i)(^|[\s,])(в|к|до)\s+\d{1,2}(?::\d{2})?($|[\s,])`,
		`(^|[\s,])\d{1,2}:\d{2}($|[\s,])`,
		`(?i)(^|[\s,])(утром|утра|днём|днем|дня|вечером|вечера|ночью|ночи)($|[\s,])`,
		`(?i)(^|[\s,])(каждый\s+день|ежедневно|каждое\s+утро|каждый\s+вечер)($|[\s,])`,
		`(?i)(^|[\s,])(не\s+срочно)($|[\s,])`,
		`(?i)(^|[\s,])(очень\s+срочно|сегодня\s+обязательно|срочно|обязательно|asap|горит|важно|желательно|на\s+неделе|когда-нибудь|потом|идея|someday)($|[\s,])`,
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		for {
			next := re.ReplaceAllString(title, " ")
			if next == title {
				break
			}
			title = next
		}
	}
	title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
	title = strings.Trim(title, " \t\r\n,")
	return title
}

func containsToken(text string, token string) bool {
	pattern := `(?i)(^|[\s,])` + regexp.QuoteMeta(token) + `($|[\s,])`
	return regexp.MustCompile(pattern).MatchString(text)
}

func countAnyToken(text string, tokens ...string) int {
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	count := 0
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, " \t\r\n,")
		if _, ok := tokenSet[field]; ok {
			count++
		}
	}
	return count
}

func nextWeekday(now time.Time, target time.Weekday) time.Time {
	days := (int(target) - int(now.Weekday()) + 7) % 7
	return dateOnly(now.AddDate(0, 0, days))
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func monthByName(name string) (time.Month, bool) {
	months := map[string]time.Month{
		"января":   time.January,
		"февраля":  time.February,
		"марта":    time.March,
		"апреля":   time.April,
		"мая":      time.May,
		"июня":     time.June,
		"июля":     time.July,
		"августа":  time.August,
		"сентября": time.September,
		"октября":  time.October,
		"ноября":   time.November,
		"декабря":  time.December,
	}
	month, ok := months[name]
	return month, ok
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
