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
			if r.Header.Get("Accept") == "text/event-stream" {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
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

func TestRunJSONOutputIntegration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newModelsServer(t, `{"data":[{"id":"m1"}]}`)
	defer ts.Close()

	cfg := config.Default()
	cfg.SelectedProvider = "demo"
	cfg.SelectedModel = "m1"
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: ts.URL + "/v1", APIKeyEnv: "DEMO_KEY"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEMO_KEY", "secret")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--json", "say hi"}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := stdout.String(); !containsAll(got, `"ok":true`, `"message":"hello"`, `"provider":"demo"`, `"model":"m1"`) {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunNDJSONOutputIntegration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newModelsServer(t, `{"data":[{"id":"m1"}]}`)
	defer ts.Close()

	cfg := config.Default()
	cfg.SelectedProvider = "demo"
	cfg.SelectedModel = "m1"
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: ts.URL + "/v1", APIKeyEnv: "DEMO_KEY"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEMO_KEY", "secret")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--ndjson", "say hi"}, bytes.NewBuffer(nil), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := stdout.String(); !containsAll(got, `"type":"started"`, `"type":"assistant_delta","delta":"hel"`, `"type":"assistant_delta","delta":"lo"`, `"type":"assistant_done","message":"hello"`, `"type":"done"`) {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunJSONErrorSuppressesHumanStderr(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"run", "--json", "say hi"}, bytes.NewBuffer(nil), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := stdout.String(); !containsAll(got, `"ok":false`, `"error":`) {
		t.Fatalf("stdout = %q", got)
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
