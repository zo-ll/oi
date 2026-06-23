// Package config manages oi's on-disk configuration and auth persistence.
// Config and Auth are loaded from JSON files in standard XDG directories
// (config.json and auth.json under $XDG_CONFIG_HOME/oi).
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zo-ll/oi/internal/oauth"
)

// ProviderConfig defines one OpenAI-compatible provider profile.
type ProviderConfig struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

// AgentConfig holds global runtime defaults.
type AgentConfig struct {
	MaxToolOutputBytes    int    `json:"max_tool_output_bytes,omitempty"`
	ToolTimeoutSeconds    int    `json:"tool_timeout_seconds,omitempty"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds,omitempty"`
	ApprovalMode          string `json:"approval_mode,omitempty"`
	ReasoningEffort       string `json:"reasoning_effort,omitempty"`
	AutoCompactThreshold  int    `json:"auto_compact_threshold,omitempty"`
}

// Config is the top-level user configuration document.
type Config struct {
	SelectedProvider string                    `json:"selected_provider,omitempty"`
	SelectedModel    string                    `json:"selected_model,omitempty"`
	Providers        map[string]ProviderConfig `json:"providers,omitempty"`
	Agent            AgentConfig               `json:"agent,omitempty"`
}

type diskConfig struct {
	SelectedProvider      string                    `json:"selected_provider,omitempty"`
	SelectedModel         string                    `json:"selected_model,omitempty"`
	LegacyDefaultProvider string                    `json:"default_provider,omitempty"`
	LegacyDefaultModel    string                    `json:"default_model,omitempty"`
	Providers             map[string]ProviderConfig `json:"providers,omitempty"`
	Agent                 AgentConfig               `json:"agent,omitempty"`
}

// Auth stores provider API keys and refreshable OAuth credentials.
type Auth struct {
	Keys  map[string]string                       `json:"keys,omitempty"`
	OAuth map[string]oauth.OpenAICodexCredentials `json:"oauth,omitempty"`
}

// Selection is the fully resolved runtime provider selection.
type Selection struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKey    string
	AccountID string
}

// Default returns config defaults suitable for a minimal build.
func Default() *Config {
	return &Config{
		Providers: make(map[string]ProviderConfig),
		Agent: AgentConfig{
			MaxToolOutputBytes:    64 * 1024,
			ToolTimeoutSeconds:    20,
			RequestTimeoutSeconds: 600,
			ApprovalMode:          "auto",
		},
	}
}

// ConfigDir returns the oi config directory.
func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "oi")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "oi")
}

// StateDir returns the oi state directory.
func StateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "oi")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "oi")
}

// ConfigPath returns the user config file path.
func ConfigPath() string { return filepath.Join(ConfigDir(), "config.json") }

// AuthPath returns the auth file path.
func AuthPath() string { return filepath.Join(ConfigDir(), "auth.json") }

// SessionsDir returns the session storage directory.
func SessionsDir() string { return filepath.Join(StateDir(), "sessions") }

// LogsDir returns the log storage directory.
func LogsDir() string { return filepath.Join(StateDir(), "logs") }

// Load reads config.json if present, otherwise returns defaults.
func Load() (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	var disk diskConfig
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ConfigPath(), err)
	}
	cfg.SelectedProvider = firstNonEmpty(disk.SelectedProvider, disk.LegacyDefaultProvider)
	cfg.SelectedModel = firstNonEmpty(disk.SelectedModel, disk.LegacyDefaultModel)
	cfg.Providers = disk.Providers
	cfg.Agent = disk.Agent
	cfg.applyDefaults()
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]ProviderConfig)
	}
	return cfg, nil
}

// LoadAuth reads auth.json if present.
func LoadAuth() (*Auth, error) {
	auth := &Auth{Keys: make(map[string]string), OAuth: make(map[string]oauth.OpenAICodexCredentials)}
	data, err := os.ReadFile(AuthPath())
	if err != nil {
		if os.IsNotExist(err) {
			return auth, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, auth); err != nil {
		return nil, fmt.Errorf("parse %s: %w", AuthPath(), err)
	}
	if auth.Keys == nil {
		auth.Keys = make(map[string]string)
	}
	if auth.OAuth == nil {
		auth.OAuth = make(map[string]oauth.OpenAICodexCredentials)
	}
	return auth, nil
}

// Save writes config.json.
func Save(c *Config) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(ConfigPath(), data, 0o644)
}

// SaveAuth writes auth.json using private file permissions.
func SaveAuth(auth *Auth) error {
	if auth == nil {
		auth = &Auth{Keys: make(map[string]string), OAuth: make(map[string]oauth.OpenAICodexCredentials)}
	}
	if auth.Keys == nil {
		auth.Keys = make(map[string]string)
	}
	if auth.OAuth == nil {
		auth.OAuth = make(map[string]oauth.OpenAICodexCredentials)
	}
	clean := &Auth{
		Keys:  make(map[string]string, len(auth.Keys)),
		OAuth: make(map[string]oauth.OpenAICodexCredentials, len(auth.OAuth)),
	}
	for name, key := range auth.Keys {
		if key = firstNonEmpty(key); key != "" {
			clean.Keys[name] = key
		}
	}
	for name, cred := range auth.OAuth {
		if firstNonEmpty(cred.Access, cred.Refresh, cred.AccountID) == "" || cred.ExpiresAt.IsZero() {
			continue
		}
		clean.OAuth[name] = cred
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(AuthPath(), data, 0o600)
}

// Validate checks structural config issues.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	for name, p := range c.Providers {
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required", name)
		}
	}
	if c.SelectedProvider != "" {
		if _, ok := c.Providers[c.SelectedProvider]; !ok {
			return fmt.Errorf("selected_provider %q is not defined in providers", c.SelectedProvider)
		}
	}
	return nil
}

// ProviderNames returns all provider names sorted.
func ProviderNames(c *Config) []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveSelection merges CLI/env/auth/config values.
func ResolveSelection(c *Config, auth *Auth, cliProvider, cliModel, cliKey string) (Selection, error) {
	if c == nil {
		c = Default()
	}
	if auth == nil {
		auth = &Auth{Keys: make(map[string]string), OAuth: make(map[string]oauth.OpenAICodexCredentials)}
	}
	sel := Selection{
		Provider: firstNonEmpty(cliProvider, c.SelectedProvider),
		Model:    firstNonEmpty(cliModel, c.SelectedModel),
	}
	if sel.Provider == "" {
		return sel, nil
	}
	pc, ok := c.Providers[sel.Provider]
	if !ok {
		return sel, fmt.Errorf("provider %q not found", sel.Provider)
	}
	sel.BaseURL = pc.BaseURL
	sel.APIKey = firstNonEmpty(
		cliKey,
		os.Getenv(pc.APIKeyEnv),
		auth.Keys[sel.Provider],
	)
	if sel.APIKey == "" {
		if err := hydrateOAuthSelection(auth, &sel); err != nil {
			return sel, err
		}
	}
	return sel, nil
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.Agent.MaxToolOutputBytes == 0 {
		c.Agent.MaxToolOutputBytes = def.Agent.MaxToolOutputBytes
	}
	if c.Agent.ToolTimeoutSeconds == 0 {
		c.Agent.ToolTimeoutSeconds = def.Agent.ToolTimeoutSeconds
	}
	if c.Agent.RequestTimeoutSeconds == 0 {
		c.Agent.RequestTimeoutSeconds = def.Agent.RequestTimeoutSeconds
	}
	if c.Agent.ApprovalMode == "" {
		c.Agent.ApprovalMode = def.Agent.ApprovalMode
	}
}

func hydrateOAuthSelection(auth *Auth, sel *Selection) error {
	if auth == nil || sel == nil {
		return nil
	}
	switch sel.Provider {
	case "openai-codex":
		cred, ok := auth.OAuth[sel.Provider]
		if !ok {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		fresh, changed, err := oauth.RefreshOpenAICodexTokenIfNeeded(ctx, cred)
		if err != nil {
			return err
		}
		if changed {
			auth.OAuth[sel.Provider] = fresh
			if err := SaveAuth(auth); err != nil {
				return err
			}
		}
		sel.APIKey = fresh.Access
		sel.AccountID = fresh.AccountID
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
