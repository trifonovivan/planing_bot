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
	appmetrics "planing_bot/internal/metrics"
	"planing_bot/internal/parser"
)

const defaultTimeout = 2 * time.Second

var ErrModelRejectedMessage = errors.New("ml parser rejected message")

type Client struct {
	endpoint   string
	httpClient *http.Client
	metrics    *appmetrics.Registry
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

func (r parseResponse) metricStatus() string {
	status := strings.TrimSpace(r.Output.Status)
	if status == "" {
		return "empty_status"
	}
	return status
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

func WithMetrics(registry *appmetrics.Registry) Option {
	return func(c *Client) {
		c.metrics = registry
	}
}

func (c *Client) Parse(ctx context.Context, text string, now time.Time, location *time.Location) (parser.ParseResult, error) {
	if strings.TrimSpace(c.endpoint) == "" {
		return fallbackParse(text, now, location, "ml_parser_not_configured")
	}
	if location == nil {
		location = time.Local
	}
	ruleResult, ruleErr := parser.Parse(text, now, location)

	requestStart := time.Now()
	body, err := json.Marshal(parseRequest{
		Text:     text,
		BaseTime: now.In(location).Format(time.RFC3339),
	})
	if err != nil {
		c.observeRequest(requestStart, "fallback", "marshal_error", "")
		return fallbackParse(text, now, location, "ml_parser_request_marshal_failed")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		c.observeRequest(requestStart, "fallback", "request_error", "")
		return fallbackParse(text, now, location, "ml_parser_request_create_failed")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.observeRequest(requestStart, "fallback", "transport_error", "")
		return fallbackParse(text, now, location, "ml_parser_http_failed: "+err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		c.observeRequest(requestStart, "fallback", fmt.Sprintf("http_%d", resp.StatusCode), "")
		return fallbackParse(text, now, location, fmt.Sprintf("ml_parser_http_status_%d", resp.StatusCode))
	}

	limited := io.LimitReader(resp.Body, 1<<20)
	var parsed parseResponse
	if err := json.NewDecoder(limited).Decode(&parsed); err != nil {
		c.observeRequest(requestStart, "fallback", "decode_error", "")
		return fallbackParse(text, now, location, "ml_parser_decode_failed: "+err.Error())
	}

	result, err := parsed.toParseResult(location)
	if err != nil {
		c.observeRequest(requestStart, "rejected", parsed.metricStatus(), parsed.TimeSource)
		return result, err
	}
	result = preferRuleSchedule(result, ruleResult, ruleErr)
	result.Warnings = append(result.Warnings, "ml_parser_used")
	if parsed.TimeSource != "" {
		result.Warnings = append(result.Warnings, "ml_parser_time_source: "+parsed.TimeSource)
	}
	c.observeRequest(requestStart, "success", parsed.metricStatus(), parsed.TimeSource)
	return result, nil
}

func (c *Client) observeRequest(start time.Time, result string, status string, timeSource string) {
	if c.metrics == nil {
		return
	}
	labels := appmetrics.Labels{
		"result":      metricLabelValue(result, "unknown"),
		"status":      metricLabelValue(status, "unknown"),
		"time_source": metricLabelValue(timeSource, "none"),
	}
	c.metrics.Inc("ml_parser_request_total", labels)
	c.metrics.ObserveDuration("ml_parser_request_duration_seconds", labels, start)
}

func metricLabelValue(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func preferRuleSchedule(result parser.ParseResult, ruleResult parser.ParseResult, ruleErr error) parser.ParseResult {
	if ruleErr != nil {
		return result
	}
	if ruleResult.DueAt != nil {
		result.DueAt = ruleResult.DueAt
		result.RemindAt = ruleResult.RemindAt
		if ruleResult.RecurrenceRule != nil {
			result.RecurrenceRule = ruleResult.RecurrenceRule
		}
		result.Warnings = append(result.Warnings, "rule_parser_schedule_used")
	}
	if result.Category == nil && ruleResult.Category != nil {
		result.Category = ruleResult.Category
		result.Warnings = append(result.Warnings, "rule_parser_category_used")
	}
	return result
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
