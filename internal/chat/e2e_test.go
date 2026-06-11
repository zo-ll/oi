package chat

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
)

func newChatOpenAITestServer(t *testing.T, models []string, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[`)
			for i, model := range models {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%q}`, model)
			}
			fmt.Fprint(w, `]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`, reply)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
}

func newChatOpenCodeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Fatalf("models authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"deepseek-v4-flash"},{"id":"minimax-m3"}]}`)
		case "/v1/chat/completions":
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Fatalf("chat authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"hello from deepseek"}}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`)
		case "/v1/messages":
			if got := r.Header.Get("x-api-key"); got != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"type":"error","error":{"type":"AuthError","message":"Missing API key."}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"content":[{"type":"text","text":"hello from minimax"}],"usage":{"input_tokens":11,"output_tokens":4}}`)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
}

func TestChatRunLineModeEndToEndSaveLoadCompact(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newChatOpenAITestServer(t, []string{"alpha-model"}, "hello from alpha")
	defer ts.Close()

	cfg := config.Default()
	cfg.SelectedProvider = "alpha"
	cfg.SelectedModel = "alpha-model"
	cfg.Providers["alpha"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAuth(&config.Auth{Keys: map[string]string{"alpha": "secret"}}); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("/stream\n2\nhi\n/compact\n/save\nsnap\n/session\n1\n/exit\n")
	var out strings.Builder
	if err := Run(nil, input, &out, Dependencies{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"hello from alpha", "session compacted", "saved:", "loaded:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
	path := filepath.Join(config.SessionsDir(), "snap.json")
	saved, err := session.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 1 || saved.Messages[0].Kind != "summary" {
		t.Fatalf("saved session = %+v", saved.Messages)
	}
}

func TestChatRunLineModeModelSwitchEndToEnd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	alpha := newChatOpenAITestServer(t, []string{"alpha-model"}, "hello from alpha")
	defer alpha.Close()
	beta := newChatOpenAITestServer(t, []string{"beta-model"}, "hello from beta")
	defer beta.Close()

	cfg := config.Default()
	cfg.SelectedProvider = "alpha"
	cfg.SelectedModel = "alpha-model"
	cfg.Providers["alpha"] = config.ProviderConfig{BaseURL: alpha.URL + "/v1"}
	cfg.Providers["beta"] = config.ProviderConfig{BaseURL: beta.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAuth(&config.Auth{Keys: map[string]string{"alpha": "secret-a", "beta": "secret-b"}}); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("/model\n2\n/stream\n2\nhi\n/exit\n")
	var out strings.Builder
	if err := Run(nil, input, &out, Dependencies{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"model set to beta-model", "hello from beta"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
	cfg2, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SelectedProvider != "beta" || cfg2.SelectedModel != "beta-model" {
		t.Fatalf("cfg = %+v", cfg2)
	}
}

func TestChatRunLineModeLoginThenSwitchAcrossOpenCodeBackends(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newChatOpenCodeTestServer(t)
	defer ts.Close()

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	deps := Dependencies{Login: func(args []string, in io.Reader, out io.Writer) error {
		reader := bufio.NewReader(in)
		key, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		key = strings.TrimSpace(key)
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]config.ProviderConfig)
		}
		cfg.Providers["opencode-go"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
		if err := config.Save(cfg); err != nil {
			return err
		}
		return config.SaveAuth(&config.Auth{Keys: map[string]string{"opencode-go": key}})
	}}

	input := strings.NewReader("/login\napi\nopencode-go\nsecret\n/stream\n2\n/model\ndeepseek-v4-flash\nhi\n/model\nminimax-m3\nhi again\n/exit\n")
	var out strings.Builder
	if err := Run(nil, input, &out, deps); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"login saved; use /model",
		"model set to deepseek-v4-flash",
		"hello from deepseek",
		"model set to minimax-m3",
		"hello from minimax",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
	cfg2, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SelectedProvider != "opencode-go" || cfg2.SelectedModel != "minimax-m3" {
		t.Fatalf("cfg = %+v", cfg2)
	}
}

func TestChatRunLineModeLoginRefreshesActiveProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newChatOpenCodeTestServer(t)
	defer ts.Close()

	cfg := config.Default()
	cfg.SelectedProvider = "opencode-go"
	cfg.SelectedModel = "minimax-m3"
	cfg.Providers["opencode-go"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	deps := Dependencies{Login: func(args []string, in io.Reader, out io.Writer) error {
		reader := bufio.NewReader(in)
		key, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		key = strings.TrimSpace(key)
		return config.SaveAuth(&config.Auth{Keys: map[string]string{"opencode-go": key}})
	}}

	input := strings.NewReader("/login\napi\nopencode-go\nsecret\n/stream\n2\nhi\n/exit\n")
	var out strings.Builder
	if err := Run(nil, input, &out, deps); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Provider opencode-go is not ready:",
		"login saved; active provider reloaded",
		"hello from minimax",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in output:\n%s", want, text)
		}
	}
}

func TestChatRunLineModeRejectsUnadvertisedOpenCodeModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ts := newChatOpenCodeTestServer(t)
	defer ts.Close()

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	deps := Dependencies{Login: func(args []string, in io.Reader, out io.Writer) error {
		reader := bufio.NewReader(in)
		key, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		key = strings.TrimSpace(key)
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]config.ProviderConfig)
		}
		cfg.Providers["opencode-go"] = config.ProviderConfig{BaseURL: ts.URL + "/v1"}
		if err := config.Save(cfg); err != nil {
			return err
		}
		return config.SaveAuth(&config.Auth{Keys: map[string]string{"opencode-go": key}})
	}}

	input := strings.NewReader("/login\napi\nopencode-go\nsecret\n/model\ngrok-build-0.1\n/exit\n")
	var out strings.Builder
	if err := Run(nil, input, &out, deps); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "error: ready model not found: grok-build-0.1") {
		t.Fatalf("missing unavailable-model error in output:\n%s", text)
	}
	cfg2, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SelectedModel == "grok-build-0.1" {
		t.Fatalf("cfg = %+v", cfg2)
	}
}

func TestChatRunLineModeNoProviderStartupEndToEnd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Run(nil, strings.NewReader("/exit\n"), &out, Dependencies{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No provider configured. Use /login") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := os.Stat(config.SessionsDir()); err != nil {
		t.Fatal(err)
	}
}
