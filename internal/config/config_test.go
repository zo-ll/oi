package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/oauth"
)

func TestConfigAndStateDirUseXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/oi-config-home")
	t.Setenv("XDG_STATE_HOME", "/tmp/oi-state-home")

	if got := ConfigDir(); got != filepath.Join("/tmp/oi-config-home", "oi") {
		t.Fatalf("ConfigDir() = %q", got)
	}
	if got := StateDir(); got != filepath.Join("/tmp/oi-state-home", "oi") {
		t.Fatalf("StateDir() = %q", got)
	}
}

func TestResolveSelectionPrefersCLIThenEnvThenAuth(t *testing.T) {
	cfg := &Config{
		SelectedProvider: "demo",
		SelectedModel:    "selected-model",
		Providers: map[string]ProviderConfig{
			"demo": {BaseURL: "https://example.invalid/v1", APIKeyEnv: "DEMO_KEY"},
		},
	}
	auth := &Auth{Keys: map[string]string{"demo": "auth-key"}}

	t.Setenv("DEMO_KEY", "env-key")

	sel, err := ResolveSelection(cfg, auth, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env-key", sel.APIKey)
	}
	if sel.Model != "selected-model" {
		t.Fatalf("Model = %q", sel.Model)
	}

	sel, err = ResolveSelection(cfg, auth, "demo", "cli-model", "cli-key")
	if err != nil {
		t.Fatal(err)
	}
	if sel.APIKey != "cli-key" {
		t.Fatalf("APIKey = %q, want cli-key", sel.APIKey)
	}
	if sel.Model != "cli-model" {
		t.Fatalf("Model = %q, want cli-model", sel.Model)
	}
}

func TestLoadReturnsDefaultsWhenMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ToolTimeoutSeconds == 0 {
		t.Fatal("expected default tool timeout")
	}
}

func TestLoadAuthReturnsEmptyWhenMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	auth, err := LoadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if len(auth.Keys) != 0 {
		t.Fatalf("expected empty keys, got %v", auth.Keys)
	}
}

func TestValidateRejectsUnknownSelectedProvider(t *testing.T) {
	cfg := Default()
	cfg.SelectedProvider = "missing"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadParsesConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	oiDir := filepath.Join(dir, "oi")
	if err := os.MkdirAll(oiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"selected_provider":"demo","providers":{"demo":{"base_url":"https://example.invalid/v1"}}}`
	if err := os.WriteFile(filepath.Join(oiDir, "config.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SelectedProvider != "demo" {
		t.Fatalf("SelectedProvider = %q", cfg.SelectedProvider)
	}
}

func TestLoadMigratesLegacyDefaultKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	oiDir := filepath.Join(dir, "oi")
	if err := os.MkdirAll(oiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"default_provider":"demo","default_model":"m1","providers":{"demo":{"base_url":"https://example.invalid/v1"}}}`
	if err := os.WriteFile(filepath.Join(oiDir, "config.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SelectedProvider != "demo" || cfg.SelectedModel != "m1" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSaveDoesNotWriteLegacyDefaultKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &Config{
		SelectedProvider: "demo",
		SelectedModel:    "m1",
		Providers: map[string]ProviderConfig{
			"demo": {BaseURL: "https://example.invalid/v1"},
		},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"selected_provider": "demo"`) || !strings.Contains(text, `"selected_model": "m1"`) {
		t.Fatalf("config = %s", text)
	}
	if strings.Contains(text, `"default_provider"`) || strings.Contains(text, `"default_model"`) {
		t.Fatalf("legacy keys still present: %s", text)
	}
}

func TestSaveAuthWritesPrivateFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SaveAuth(&Auth{Keys: map[string]string{"demo": "key"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(AuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o", got)
	}
}

func TestResolveSelectionUsesStoredOAuth(t *testing.T) {
	cfg := &Config{
		SelectedProvider: "openai-codex",
		SelectedModel:    "gpt-5.4",
		Providers: map[string]ProviderConfig{
			"openai-codex": {BaseURL: "https://chatgpt.com/backend-api"},
		},
	}
	auth := &Auth{OAuth: map[string]oauth.OpenAICodexCredentials{
		"openai-codex": {
			Access:    "tok",
			Refresh:   "ref",
			AccountID: "acct",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}}
	sel, err := ResolveSelection(cfg, auth, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.APIKey != "tok" || sel.AccountID != "acct" {
		t.Fatalf("selection = %+v", sel)
	}
}
