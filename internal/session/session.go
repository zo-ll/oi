package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ToolCall is one persisted assistant tool invocation.
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Message is one persisted transcript entry.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Reasoning  string     `json:"reasoning,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// Session stores transcript and runtime metadata.
type Session struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages,omitempty"`
}

// New constructs a new session with a generated ID.
func New(providerName, model, cwd string) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:        now.Format("20060102-150405"),
		Provider:  providerName,
		Model:     model,
		CWD:       cwd,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Save writes a session JSON file and returns its path.
func Save(dir string, s *Session) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil session")
	}
	if s.ID == "" {
		s.ID = time.Now().UTC().Format("20060102-150405")
	}
	s.UpdatedAt = time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, s.ID+".json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads one session JSON file.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Info is a lightweight session listing record.
type Info struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Preview   string    `json:"preview,omitempty"`
	Path      string    `json:"path"`
}

// List returns saved sessions sorted by most recently updated first.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		s, err := Load(path)
		if err != nil {
			continue
		}
		items = append(items, Info{
			ID:        s.ID,
			Provider:  s.Provider,
			Model:     s.Model,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
			Preview:   sessionPreview(s),
			Path:      path,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func sessionPreview(s *Session) string {
	if s == nil {
		return ""
	}
	for _, msg := range s.Messages {
		if msg.Role != "user" {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		text = strings.ReplaceAll(text, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		if len(text) > 48 {
			text = text[:45] + "..."
		}
		return text
	}
	if s.ID != "" {
		return s.ID
	}
	return "(empty)"
}
