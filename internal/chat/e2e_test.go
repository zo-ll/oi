package chat

import (
	"fmt"
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

	input := strings.NewReader("/stream off\nhi\n/compact\n/save snap\n/load snap\n/exit\n")
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

	input := strings.NewReader("/model beta-model\n/stream off\nhi\n/exit\n")
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
