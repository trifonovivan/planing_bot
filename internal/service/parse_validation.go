package service

import (
	"strings"
	"time"
	"unicode"

	"planing_bot/internal/domain"
	"planing_bot/internal/parser"
)

type ParseDecisionType string

const (
	ParseDecisionAccept             ParseDecisionType = "accept"
	ParseDecisionReject             ParseDecisionType = "reject"
	ParseDecisionNeedsClarification ParseDecisionType = "needs_clarification"
	ParseDecisionAcceptWithWarnings ParseDecisionType = "accept_with_warnings"
)

type ParseClarificationAction string

const (
	ParseActionCreateWithoutDue ParseClarificationAction = "create_without_due"
	ParseActionRemoveReminder   ParseClarificationAction = "remove_reminder"
)

type ParseValidationDecision struct {
	Decision ParseDecisionType
	Reason   string
	Message  string
	Warnings []string
	Actions  []ParseClarificationAction
}

type ParseClarificationError struct {
	InputText      string
	TaskText       string
	AssigneeUserID int64
	Decision       ParseValidationDecision
	Parse          parser.ParseResult
}

func (e *ParseClarificationError) Error() string {
	return "task parse needs clarification"
}

type ParseRejectedError struct {
	Decision ParseValidationDecision
	Parse    parser.ParseResult
}

func (e *ParseRejectedError) Error() string {
	return "task parse rejected"
}

func validateParsedTask(inputText string, parsed parser.ParseResult, now time.Time) ParseValidationDecision {
	warnings := append([]string{}, parsed.Warnings...)
	if looksLikeMultipleTasks(inputText) {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "multiple_tasks",
			Message:  "Похоже, тут несколько задач. Создать одну или лучше прислать по отдельности?",
			Warnings: warnings,
		}
	}
	if isBadTitle(parsed.Title) {
		return ParseValidationDecision{
			Decision: ParseDecisionReject,
			Reason:   "bad_title",
			Message:  "Не понял задачу. Напиши чуть подробнее.",
			Warnings: warnings,
		}
	}
	if parsed.RemindAt != nil && parsed.DueAt != nil && parsed.RemindAt.After(*parsed.DueAt) {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "remind_after_due",
			Message:  "Напоминание получилось позже срока. Когда напомнить?",
			Warnings: warnings,
			Actions:  []ParseClarificationAction{ParseActionRemoveReminder},
		}
	}
	if parsed.DueAt != nil && parsed.DueAt.Before(now) && !hasExplicitPastMarker(inputText) {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "due_at_in_past",
			Message:  "Срок получился в прошлом. Создать без срока или указать другую дату?",
			Warnings: warnings,
			Actions:  []ParseClarificationAction{ParseActionCreateWithoutDue},
		}
	}
	if !validPriority(parsed.Priority) {
		return ParseValidationDecision{
			Decision: ParseDecisionReject,
			Reason:   "invalid_priority",
			Message:  "Не понял приоритет задачи. Напиши задачу еще раз.",
			Warnings: warnings,
		}
	}
	if parsed.RecurrenceRule != nil && *parsed.RecurrenceRule != domain.RecurrenceDaily {
		return ParseValidationDecision{
			Decision: ParseDecisionReject,
			Reason:   "unsupported_repeat",
			Message:  "Пока умею создавать только ежедневные повторы. Напиши задачу без повтора или с “каждый день”.",
			Warnings: warnings,
		}
	}
	if parsed.RecurrenceRule != nil && parsed.RemindAt == nil {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "repeat_without_next_reminder",
			Message:  "Не понял, когда напоминать по повторяющейся задаче. Укажи дату или время.",
			Warnings: warnings,
		}
	}
	if parsed.Category != nil {
		normalized := strings.TrimSpace(*parsed.Category)
		if normalized == "" || strings.EqualFold(normalized, "unknown") {
			return ParseValidationDecision{
				Decision: ParseDecisionAcceptWithWarnings,
				Reason:   "category_normalized_to_nil",
				Warnings: append(warnings, "category_normalized_to_nil"),
			}
		}
	}
	if parsed.Confidence > 0 && parsed.Confidence < 0.35 {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "low_confidence",
			Message:  "Не понял задачу. Напиши чуть подробнее.",
			Warnings: warnings,
		}
	}
	if hasWarningPrefix(parsed.Warnings, "ml_parser_clarification: missing_due_at") && parsed.DueAt == nil && parsed.RecurrenceRule == nil {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "missing_due_at",
			Message:  "Не понял срок. Создать без срока или указать дату?",
			Warnings: warnings,
			Actions:  []ParseClarificationAction{ParseActionCreateWithoutDue},
		}
	}
	if len(warnings) > 0 {
		return ParseValidationDecision{Decision: ParseDecisionAcceptWithWarnings, Reason: "warnings", Warnings: warnings}
	}
	return ParseValidationDecision{Decision: ParseDecisionAccept}
}

func validateParseFailure(inputText string, parsed parser.ParseResult) ParseValidationDecision {
	warnings := append([]string{}, parsed.Warnings...)
	if looksLikeMultipleTasks(inputText) || hasWarningPrefix(warnings, "ml_parser_clarification: multiple_tasks") {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "multiple_tasks",
			Message:  "Похоже, тут несколько задач. Создать одну или лучше прислать по отдельности?",
			Warnings: warnings,
		}
	}
	if hasWarningPrefix(warnings, "ml_parser_clarification: missing_due_at") {
		return ParseValidationDecision{
			Decision: ParseDecisionNeedsClarification,
			Reason:   "missing_due_at",
			Message:  "Не понял срок. Создать без срока или указать дату?",
			Warnings: warnings,
			Actions:  []ParseClarificationAction{ParseActionCreateWithoutDue},
		}
	}
	return ParseValidationDecision{
		Decision: ParseDecisionReject,
		Reason:   "bad_title",
		Message:  "Не понял задачу. Напиши чуть подробнее.",
		Warnings: warnings,
	}
}

func applyParseAction(parsed parser.ParseResult, action ParseClarificationAction) parser.ParseResult {
	switch action {
	case ParseActionCreateWithoutDue:
		parsed.DueAt = nil
		parsed.RemindAt = nil
		parsed.Warnings = append(parsed.Warnings, "clarification_create_without_due")
	case ParseActionRemoveReminder:
		parsed.RemindAt = nil
		parsed.Warnings = append(parsed.Warnings, "clarification_remove_reminder")
	}
	return parsed
}

func isBadTitle(title string) bool {
	title = strings.TrimSpace(strings.ToLower(title))
	if title == "" {
		return true
	}
	hasLetter := false
	for _, r := range title {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return true
	}
	serviceWords := map[string]struct{}{
		"сегодня": {}, "завтра": {}, "послезавтра": {}, "вчера": {}, "утром": {}, "вечером": {},
		"днем": {}, "ночью": {}, "срочно": {}, "важно": {}, "напомни": {}, "напомнить": {},
		"задача": {}, "дело": {}, "надо": {}, "нужно": {}, "каждый": {}, "каждое": {},
	}
	meaningful := 0
	for _, field := range strings.Fields(title) {
		field = strings.Trim(field, ".,!?;:()[]«»\"'")
		if field == "" || looksLikeClock(field) {
			continue
		}
		if _, ok := serviceWords[field]; ok {
			continue
		}
		meaningful++
	}
	return meaningful == 0
}

func looksLikeClock(value string) bool {
	if strings.Contains(value, ":") {
		return true
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(value) <= 4
}

func looksLikeMultipleTasks(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.Count(lower, "\n") >= 1 {
		return true
	}
	if strings.Count(lower, ";") >= 1 {
		return true
	}
	markers := []string{"1.", "2.", "- ", " и еще ", " ещё "}
	seen := 0
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			seen++
		}
	}
	return seen >= 2
}

func hasExplicitPastMarker(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "вчера") || strings.Contains(lower, "прошл") || strings.Contains(lower, "позавчера")
}

func validPriority(priority domain.Priority) bool {
	switch priority {
	case domain.PriorityP1, domain.PriorityP2, domain.PriorityP3, domain.PriorityP4:
		return true
	default:
		return false
	}
}

func hasWarningPrefix(warnings []string, prefix string) bool {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}
