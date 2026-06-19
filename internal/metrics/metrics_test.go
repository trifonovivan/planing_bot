package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerRendersPrometheusMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.Inc("task_created_total", Labels{"workspace_id": "1", "user_id": "2", "priority": "p2", "category": "Дача"})
	registry.SetGauge("tasks_active_total", Labels{"workspace_id": "1"}, 3)
	registry.Observe("parser_duration_seconds", nil, 0.12)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE task_created_total counter",
		`task_created_total{category="Дача",priority="p2",user_id="2",workspace_id="1"} 1`,
		"# TYPE tasks_active_total gauge",
		`tasks_active_total{workspace_id="1"} 3`,
		`parser_duration_seconds_bucket{le="+Inf"} 1`,
		"parser_duration_seconds_sum 0.12",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body does not contain %q:\n%s", want, body)
		}
	}
}

func TestObserveDuration(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveDuration("telegram_update_duration_seconds", nil, time.Now().Add(-time.Millisecond))

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	registry.Handler().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "telegram_update_duration_seconds_count 1") {
		t.Fatalf("unexpected body:\n%s", rec.Body.String())
	}
}
