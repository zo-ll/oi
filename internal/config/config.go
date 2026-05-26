package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ProviderConfig defines one OpenAI-compatible provider profile.
type ProviderConfig struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

// AgentConfig holds global runtime defaults.
type AgentConfig struct {
	MaxSteps           int    `json:"max_steps,omitempty"`
	MaxToolOutputBytes int    `json:"max_tool_output_bytes,omitempty"`
	ToolTimeoutSeconds int    `json:"tool_timeout_seconds,omitempty"`
	ApprovalMode       string `json:"approval_mode,omitempty"`
}

// Config is the top-level user configuration document.
type Config struct {
	DefaultProvider string                    `json:"default_provider,omitempty"`
	DefaultModel    string                    `json:"default_model,omitempty"`
	Providers       map[string]ProviderConfig `json:"providers,omitempty"`
	Agent           AgentConfig               `json:"agent,omitempty"`
}

// Auth stores provider API keys when env vars are not used.
type Auth struct {
	Keys map[string]string `json:"keys,omitempty"`
}

// Selection is the fully resolved runtime provider selection.
type Selection struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

// Default returns config defaults suitable for a minimal build.
func Default() *Config {
	return &Config{
		Providers: make(map[string]ProviderConfig),
		Agent: AgentConfig{
			MaxSteps:           12,
			MaxToolOutputBytes: 64 * 1024,
			ToolTimeoutSeconds: 20,
			ApprovalMode:       "prompt",
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
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ConfigPath(), err)
	}
	cfg.applyDefaults()
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]ProviderConfig)
	}
	return cfg, nil
}

// LoadAuth reads auth.json if present.
func LoadAuth() (*Auth, error) {
	auth := &Auth{Keys: make(map[string]string)}
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
	return auth, nil
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
	if c.DefaultProvider != "" {
		if _, ok := c.Providers[c.DefaultProvider]; !ok {
			return fmt.Errorf("default_provider %q is not defined in providers", c.DefaultProvider)
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
		auth = &Auth{Keys: make(map[string]string)}
	}
	sel := Selection{
		Provider: firstNonEmpty(cliProvider, c.DefaultProvider),
		Model:    firstNonEmpty(cliModel, c.DefaultModel),
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
	return sel, nil
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.Agent.MaxSteps == 0 {
		c.Agent.MaxSteps = def.Agent.MaxSteps
	}
	if c.Agent.MaxToolOutputBytes == 0 {
		c.Agent.MaxToolOutputBytes = def.Agent.MaxToolOutputBytes
	}
	if c.Agent.ToolTimeoutSeconds == 0 {
		c.Agent.ToolTimeoutSeconds = def.Agent.ToolTimeoutSeconds
	}
	if c.Agent.ApprovalMode == "" {
		c.Agent.ApprovalMode = def.Agent.ApprovalMode
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
