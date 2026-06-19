package logging

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type Fields map[string]any

type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

func New(out io.Writer) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{out: out}
}

func (l *Logger) Info(event string, fields Fields) {
	l.write("info", event, nil, fields)
}

func (l *Logger) Error(event string, err error, fields Fields) {
	l.write("error", event, err, fields)
}

func (l *Logger) write(level string, event string, err error, fields Fields) {
	if l == nil {
		return
	}
	entry := make(map[string]any, len(fields)+4)
	entry["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["event"] = event
	if err != nil {
		entry["error"] = err.Error()
	}
	for key, value := range fields {
		entry[key] = value
	}

	data, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		data = []byte(`{"level":"error","event":"log_marshal_failed"}`)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(append(data, '\n'))
}
