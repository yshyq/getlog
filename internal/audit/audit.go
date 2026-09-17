package audit

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

type Logger interface {
	Log(event Event)
}

type Event map[string]any

type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{w: w}
}

func (l *JSONLogger) Log(event Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := event["timestamp"]; !ok {
		event["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_ = json.NewEncoder(l.w).Encode(event)
}
