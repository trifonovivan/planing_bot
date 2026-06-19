package mlparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"planing_bot/internal/domain"
	"planing_bot/internal/parser"
)

const defaultTimeout = 2 * time.Second

var ErrModelRejectedMessage = errors.New("ml parser rejected message")

type Client struct {
	endpoint   string
	httpClient *http.Client
}

type Option func(*Client)

type parseRequest struct {
	Text     string `json:"text"`
	BaseTime string `json:"base_time,omitempty"`
}

type parseResponse struct {
	Output          parserOutput       `json:"output"`
	Confidence      float64            `json:"confidence"`
	FieldConfidence map[string]float64 `json:"field_confidence"`
	Source          string             `json:"source"`
	TimeSource      string             `json:"time_source"`
}

type parserOutput struct {
	Title               *string `json:"title"`
	DueAt               *string `json:"due_at"`
	RemindAt            *string `json:"remind_at"`
	Priority            *string `json:"priority"`
	Category            *string `json:"category"`
	Assignee            *string `json:"assignee"`
	Repeat              *string `json:"repeat"`
	Status              string  `json:"status"`
	ClarificationReason *string `json:"clarification_reason"`
}

func New(endpoint string, opts ...Option) *Client {
	client := &Client{
		endpoint: strings.TrimSpace(endpoint),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func (c *Client) Parse(ctx context.Context, text string, now time.Time, location *time.Location) (parser.ParseResult, error) {
	if strings.TrimSpace(c.endpoint) == "" {
		return fallbackParse(text, now, location, "ml_parser_not_configured")
	}
	if location == nil {
		location = time.Local
	}

	body, err := json.Marshal(parseRequest{
		Text:     text,
		BaseTime: now.In(location).Format(time.RFC3339),
	})
	if err != nil {
		return fallbackParse(text, now, location, "ml_parser_request_marshal_failed")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fallbackParse(text, now, location, "ml_parser_request_create_failed")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fallbackParse(text, now, location, "ml_parser_http_failed: "+err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fallbackParse(text, now, location, fmt.Sprintf("ml_parser_http_status_%d", resp.StatusCode))
	}

	limited := io.LimitReader(resp.Body, 1<<20)
	var parsed parseResponse
	if err := json.NewDecoder(limited).Decode(&parsed); err != nil {
		return fallbackParse(text, now, location, "ml_parser_decode_failed: "+err.Error())
	}

	result, err := parsed.toParseResult(location)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, "ml_parser_used")
	if parsed.TimeSource != "" {
		result.Warnings = append(result.Warnings, "ml_parser_time_source: "+parsed.TimeSource)
	}
	return result, nil
}

func (r parseResponse) toParseResult(location *time.Location) (parser.ParseResult, error) {
	status := strings.TrimSpace(r.Output.Status)
	confidence := r.Confidence
	if confidence == 0 {
		confidence = 0.5
	}
	warnings := make([]string, 0)
	if r.Source != "" {
		warnings = append(warnings, "ml_parser_source: "+r.Source)
	}
	if r.Output.ClarificationReason != nil && *r.Output.ClarificationReason != "" {
		warnings = append(warnings, "ml_parser_clarification: "+*r.Output.ClarificationReason)
	}

	if status == "ignored" || status == "failed" || status == "needs_clarification" {
		return parser.ParseResult{
			Priority:   domain.PriorityP3,
			Confidence: confidence,
			Warnings:   warnings,
		}, parser.ErrEmptyTitle
	}

	title := ""
	if r.Output.Title != nil {
		title = strings.TrimSpace(*r.Output.Title)
	}
	if title == "" {
		return parser.ParseResult{
			Priority:   domain.PriorityP3,
			Confidence: confidence,
			Warnings:   warnings,
		}, parser.ErrEmptyTitle
	}

	dueAt, err := parseOptionalTime(r.Output.DueAt, location)
	if err != nil {
		warnings = append(warnings, "ml_parser_invalid_due_at: "+err.Error())
	}
	remindAt, err := parseOptionalTime(r.Output.RemindAt, location)
	if err != nil {
		warnings = append(warnings, "ml_parser_invalid_remind_at: "+err.Error())
	}
	recurrenceRule := mapRepeat(r.Output.Repeat, &warnings)

	return parser.ParseResult{
		Title:          title,
		DueAt:          dueAt,
		RemindAt:       remindAt,
		Priority:       mapPriority(r.Output.Priority),
		Category:       mapCategory(r.Output.Category),
		RecurrenceRule: recurrenceRule,
		Confidence:     confidence,
		Warnings:       warnings,
	}, nil
}

func fallbackParse(text string, now time.Time, location *time.Location, warning string) (parser.ParseResult, error) {
	result, err := parser.Parse(text, now, location)
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	result.Warnings = append(result.Warnings, "rule_parser_fallback")
	return result, err
}

func parseOptionalTime(value *string, location *time.Location) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil, err
	}
	if location != nil {
		parsed = parsed.In(location)
	}
	return &parsed, nil
}

func mapPriority(value *string) domain.Priority {
	if value == nil {
		return domain.PriorityP3
	}
	switch strings.ToLower(strings.TrimSpace(*value)) {
	case string(domain.PriorityP1):
		return domain.PriorityP1
	case string(domain.PriorityP2):
		return domain.PriorityP2
	case string(domain.PriorityP4):
		return domain.PriorityP4
	default:
		return domain.PriorityP3
	}
}

func mapCategory(value *string) *string {
	if value == nil {
		return nil
	}
	category := strings.ToLower(strings.TrimSpace(*value))
	if category == "" || category == "unknown" {
		return nil
	}
	mapped := map[string]string{
		"work":     "Работа",
		"shopping": "Покупки",
		"home":     "Дом",
		"health":   "Здоровье",
		"finance":  "Финансы",
		"car":      "Авто",
		"study":    "Учеба",
		"family":   "Семья",
		"garden":   "Дача",
		"personal": "Личное",
	}[category]
	if mapped == "" {
		mapped = *value
	}
	return &mapped
}

func mapRepeat(value *string, warnings *[]string) *domain.RecurrenceRule {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	repeat := strings.ToUpper(strings.TrimSpace(*value))
	if strings.Contains(repeat, "FREQ=DAILY") {
		rule := domain.RecurrenceDaily
		return &rule
	}
	if warnings != nil {
		*warnings = append(*warnings, "ml_parser_unsupported_repeat: "+strings.TrimSpace(*value))
	}
	return nil
}
