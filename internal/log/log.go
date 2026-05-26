package log

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger writes one JSON object per line.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONL opens a file-backed JSONL logger.
func NewJSONL(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{w: f}, nil
}

// New returns a logger over an arbitrary writer.
func New(w io.Writer) *Logger {
	return &Logger{w: w}
}

// Event writes a timestamped JSONL event.
func (l *Logger) Event(kind string, fields map[string]any) error {
	if l == nil || l.w == nil {
		return nil
	}
	rec := map[string]any{
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
		"kind": kind,
	}
	for k, v := range fields {
		rec[k] = v
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(data, '\n'))
	return err
}
