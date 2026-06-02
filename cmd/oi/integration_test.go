package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zo-ll/oi/internal/config"
)

func newModelsServer(t *testing.T, modelsJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, modelsJSON)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}]}`)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
}

func TestRunLoginIsAuthOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newModelsServer(t, `{"data":[{"id":"m1"}]}`)
	defer ts.Close()

	cfg := config.Default()
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runLogin([]string{"--provider", "demo", "--api-key", "secret"}, bytes.NewBuffer(nil), &out); err != nil {
		t.Fatal(err)
	}

	cfg2, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := config.LoadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SelectedProvider != "" || cfg2.SelectedModel != "" {
		t.Fatalf("selection mutated: %+v", cfg2)
	}
	if auth.Keys["demo"] != "secret" {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestLoadSelectionPrecedenceIntegration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("DEMO_KEY", "env-key")

	cfg := config.Default()
	cfg.SelectedProvider = "demo"
	cfg.SelectedModel = "config-model"
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: "https://example.invalid/v1", APIKeyEnv: "DEMO_KEY"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAuth(&config.Auth{Keys: map[string]string{"demo": "auth-key"}}); err != nil {
		t.Fatal(err)
	}

	_, sel, err := loadSelection(commonOptions{model: "cli-model", apiKey: "cli-key"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Provider != "demo" || sel.Model != "cli-model" || sel.APIKey != "cli-key" {
		t.Fatalf("selection = %+v", sel)
	}

	_, sel, err = loadSelection(commonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Model != "config-model" || sel.APIKey != "env-key" {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestRunInteractiveStartsWithoutProviderIntegration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{}, bytes.NewBufferString("/exit\n"), &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got == "" || !containsAll(got, "No provider configured.", "Use /login") {
		t.Fatalf("out = %q", got)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !bytes.Contains([]byte(s), []byte(part)) {
			return false
		}
	}
	return true
}
