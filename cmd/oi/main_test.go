package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/zo-ll/oi/internal/config"
)

func TestKnownProviderProfile(t *testing.T) {
	got, ok := knownProviderProfile("openai")
	if !ok {
		t.Fatal("expected known provider")
	}
	want := config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
	got, ok = knownProviderProfile("chatgpt")
	if !ok {
		t.Fatal("expected chatgpt alias")
	}
	want = config.ProviderConfig{BaseURL: "https://chatgpt.com/backend-api"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
	got, ok = knownProviderProfile("openrouter")
	if !ok {
		t.Fatal("expected openrouter profile")
	}
	want = config.ProviderConfig{BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
	got, ok = knownProviderProfile("opencode-go")
	if !ok {
		t.Fatal("expected opencode-go profile")
	}
	want = config.ProviderConfig{BaseURL: "https://opencode.ai/zen/go/v1", APIKeyEnv: "OPENCODE_API_KEY"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
}

func TestCanonicalProviderName(t *testing.T) {
	if got := canonicalProviderName("chatgpt"); got != "openai-codex" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeAPIKey(t *testing.T) {
	if got := normalizeAPIKey("Bearer sk-test\n"); got != "sk-test" {
		t.Fatalf("key = %q", got)
	}
}

type fakeLoginPrompt struct {
	bytes.Buffer
	inputs []string
}

func (f *fakeLoginPrompt) LoginPrompt(prompt string, required bool) (string, bool) {
	if len(f.inputs) == 0 {
		return "", false
	}
	s := f.inputs[0]
	f.inputs = f.inputs[1:]
	return s, true
}

func (f *fakeLoginPrompt) CancelOverlay() {}

func TestRunLoginFallsBackToStdin(t *testing.T) {
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
	if err := runLogin([]string{"--provider", "demo"}, strings.NewReader("secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "saved provider: demo") {
		t.Fatalf("out = %q", got)
	}
	auth, err := config.LoadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if auth.Keys["demo"] != "secret" {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestRunLoginUsesPromptUI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newModelsServer(t, `{"data":[{"id":"m1"}]}`)
	defer ts.Close()

	cfg := config.Default()
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	out := &fakeLoginPrompt{inputs: []string{"secret"}}
	if err := runLogin([]string{"--provider", "demo"}, strings.NewReader(""), out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "saved provider: demo") {
		t.Fatalf("out = %q", got)
	}
	auth, err := config.LoadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if auth.Keys["demo"] != "secret" {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestRunLoginPromptUICancel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newModelsServer(t, `{"data":[{"id":"m1"}]}`)
	defer ts.Close()

	cfg := config.Default()
	cfg.Providers["demo"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	out := &fakeLoginPrompt{inputs: []string{}}
	if err := runLogin([]string{"--provider", "demo"}, strings.NewReader(""), out); err == nil || !strings.Contains(err.Error(), "login canceled") {
		t.Fatalf("err = %v", err)
	}
}
