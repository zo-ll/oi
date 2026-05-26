package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Message is one persisted transcript entry.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Kind    string `json:"kind,omitempty"`
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
