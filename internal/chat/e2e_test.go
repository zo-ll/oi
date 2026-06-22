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
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
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
		"login saved; active provider reloaded",
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

type pickerRecorder struct {
	strings.Builder
	choices []string
	inputs  []string
	titles  []string
	items   [][]string
}

func (p *pickerRecorder) overlayPicker(title string, items []string) (string, bool) {
	p.titles = append(p.titles, title)
	p.items = append(p.items, append([]string(nil), items...))
	if len(p.choices) == 0 {
		return "", false
	}
	choice := p.choices[0]
	p.choices = p.choices[1:]
	if choice == "" {
		return "", false
	}
	return choice, true
}

func (p *pickerRecorder) overlayInput(title, prompt, initial string) (string, bool) {
	p.titles = append(p.titles, title)
	if len(p.inputs) == 0 {
		return "", false
	}
	text := p.inputs[0]
	p.inputs = p.inputs[1:]
	if text == "" {
		return "", false
	}
	return text, true
}

func TestLoginAndSwitchChatProviderUsesPicker(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	picker := &pickerRecorder{choices: []string{"sub  ChatGPT subscription / browser login", "openai"}}
	var gotArgs []string
	deps := Dependencies{Login: func(args []string, in io.Reader, out io.Writer) error {
		gotArgs = append([]string(nil), args...)
		body, _ := io.ReadAll(in)
		if string(body) != "" {
			t.Fatalf("expected empty browser-login stdin, got %q", string(body))
		}
		return nil
	}}
	_, _, err := loginAndSwitchChatProvider(deps, cfg, config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), picker, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "openai-codex" {
		t.Fatalf("args = %#v", gotArgs)
	}
	if len(picker.titles) != 2 || picker.titles[0] != "choose login type" || picker.titles[1] != "choose provider" {
		t.Fatalf("picker titles = %#v", picker.titles)
	}
	if !strings.Contains(picker.String(), "login saved; use /model") {
		t.Fatalf("out = %q", picker.String())
	}
}

func TestLoginAndSwitchChatProviderCancelSilent(t *testing.T) {
	picker := &pickerRecorder{choices: []string{""}}
	_, _, err := loginAndSwitchChatProvider(Dependencies{}, config.Default(), config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), picker, nil)
	if err != nil {
		t.Fatal(err)
	}
	if picker.String() != "" {
		t.Fatalf("out = %q", picker.String())
	}
}

func TestPickSessionInfo(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 30, 0, 0, time.UTC)
	infos := []session.Info{{ID: "beta", Preview: "fix auth bug", CreatedAt: now, UpdatedAt: now, Path: "/tmp/beta.json"}}
	picker := &pickerRecorder{choices: []string{sessionPickerLabel(infos[0])}}
	got, ok := pickSessionInfo(picker, infos, "load session")
	if !ok {
		t.Fatal("expected selection")
	}
	if got.Path != infos[0].Path {
		t.Fatalf("got = %+v", got)
	}
	if len(picker.titles) != 1 || picker.titles[0] != "load session" {
		t.Fatalf("titles = %#v", picker.titles)
	}
	if label := sessionPickerLabel(infos[0]); !strings.Contains(label, "fix auth bug") || !strings.Contains(label, "2026-06-11") {
		t.Fatalf("label = %q", label)
	}
}

func TestHandleChatCommandSessionUsesPicker(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Providers["openai"] = config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAuth(&config.Auth{Keys: map[string]string{"openai": "secret"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, s := range []*session.Session{{ID: "alpha", Provider: "openai", Model: "gpt-4.1", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour), Messages: []session.Message{{Role: "user", Content: "hello there"}, {Role: "assistant", Content: "hi back"}}}} {
		if _, err := session.Save(config.SessionsDir(), s); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := filteredSessions(config.SessionsDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	picker := &pickerRecorder{choices: []string{sessionPickerLabel(infos[0])}}
	rt := &agent.Runtime{Policy: workspace.Policy{Root: t.TempDir()}, Session: session.New("openai", "gpt-4.1", ".")}
	_, _, _, _, _, _, err = handleChatCommand(Dependencies{}, cfg, config.Selection{Provider: "openai", Model: "gpt-4.1"}, rt, bufio.NewReader(strings.NewReader("")), picker, "/session", true, true, toolVerbosityErrors)
	if err != nil {
		t.Fatal(err)
	}
	if len(picker.titles) != 1 || picker.titles[0] != "choose session" {
		t.Fatalf("titles = %#v", picker.titles)
	}
	text := picker.String()
	if !strings.Contains(text, "> hello there") || !strings.Contains(text, "hi back") {
		t.Fatalf("out = %q", text)
	}
}

func TestHandleChatCommandSessionCancelSilent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now().UTC()
	if _, err := session.Save(config.SessionsDir(), &session.Session{ID: "alpha", Provider: "openai", Model: "gpt-4.1", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	picker := &pickerRecorder{choices: []string{""}}
	_, _, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), picker, "/session", true, true, toolVerbosityErrors)
	if err != nil {
		t.Fatal(err)
	}
	if picker.String() != "" {
		t.Fatalf("out = %q", picker.String())
	}
}

func TestHandleChatCommandSessionRejectsArgs(t *testing.T) {
	_, _, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), &strings.Builder{}, "/session deep", true, true, toolVerbosityErrors)
	if err == nil || err.Error() != "usage: /session" {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleChatCommandRejectsArgsForExactCommands(t *testing.T) {
	for _, line := range []string{"/help now", "/status now", "/login api", "/model gpt", "/stream off", "/think high", "/tools on", "/autosave off", "/save snap", "/new x", "/clear now", "/exit now"} {
		_, _, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), &strings.Builder{}, line, true, true, toolVerbosityErrors)
		if err == nil || !strings.HasPrefix(err.Error(), "usage: ") {
			t.Fatalf("line=%q err=%v", line, err)
		}
	}
}

func TestHandleChatCommandChoicePickers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	picker := &pickerRecorder{choices: []string{"off", "on", "off", "high"}}
	rt := &agent.Runtime{ThinkingSupported: true, Session: session.New("openai", "m", ".")}
	_, _, _, streaming, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, rt, bufio.NewReader(strings.NewReader("")), picker, "/stream", true, true, toolVerbosityErrors)
	if err != nil || streaming {
		t.Fatalf("stream err=%v streaming=%v", err, streaming)
	}
	_, _, _, _, autosave, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, rt, bufio.NewReader(strings.NewReader("")), picker, "/autosave", true, false, toolVerbosityErrors)
	if err != nil || !autosave {
		t.Fatalf("autosave err=%v autosave=%v", err, autosave)
	}
	_, _, _, _, _, tools, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, rt, bufio.NewReader(strings.NewReader("")), picker, "/tools", true, true, toolVerbosityErrors)
	if err != nil || tools != toolVerbosityOff {
		t.Fatalf("tools err=%v tools=%v", err, tools)
	}
	_, _, _, _, _, _, err = handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, rt, bufio.NewReader(strings.NewReader("")), picker, "/think", true, true, toolVerbosityErrors)
	if err != nil || rt.ThinkingLevel != "high" || rt.Session.ThinkingLevel != "high" {
		t.Fatalf("think err=%v level=%q session=%q", err, rt.ThinkingLevel, rt.Session.ThinkingLevel)
	}
	if got := strings.Join(picker.titles, " | "); got != "choose streaming mode | choose autosave mode | choose tool visibility | choose thinking level" {
		t.Fatalf("titles=%q", got)
	}
}

func TestHandleChatCommandSaveUsesOverlayInput(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	picker := &pickerRecorder{inputs: []string{"snap"}}
	rt := &agent.Runtime{Policy: workspace.Policy{Root: t.TempDir()}, Session: session.New("openai", "gpt-4.1", ".")}
	_, _, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{Provider: "openai", Model: "gpt-4.1"}, rt, bufio.NewReader(strings.NewReader("")), picker, "/save", true, true, toolVerbosityErrors)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(picker.titles, " | "); got != "save session" {
		t.Fatalf("titles=%q", got)
	}
	if !strings.Contains(picker.String(), "saved:") {
		t.Fatalf("out=%q", picker.String())
	}
}
