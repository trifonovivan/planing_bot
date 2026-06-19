package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLoggerWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf)

	logger.Info("task_created", Fields{"user_id": 1, "workspace_id": 2})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log is not json: %v", err)
	}
	if entry["level"] != "info" || entry["event"] != "task_created" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}
