package chat

import (
	"bufio"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type fakeChatProvider struct{ models []provider.Model }

func (f *fakeChatProvider) Name() string    { return "fake" }
func (f *fakeChatProvider) Model() string   { return "m" }
func (f *fakeChatProvider) SetModel(string) {}
func (f *fakeChatProvider) Chat(context.Context, provider.Request) (provider.Response, error) {
	return provider.Response{}, nil
}
func (f *fakeChatProvider) ChatStream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, nil
}
func (f *fakeChatProvider) ListModels(context.Context) ([]provider.Model, error) {
	return f.models, nil
}

func TestResolveSessionPath(t *testing.T) {
	dir := "/tmp/sessions"
	if got := resolveSessionPath(dir, "abc"); got != filepath.Join(dir, "abc.json") {
		t.Fatalf("got %q", got)
	}
	if got := resolveSessionPath(dir, "abc.json"); got != filepath.Join(dir, "abc.json") {
		t.Fatalf("got %q", got)
	}
}

func TestValidateSessionName(t *testing.T) {
	if err := validateSessionName("good-name"); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionName(""); err == nil {
		t.Fatal("expected error for empty name")
	}
	if err := validateSessionName("bad/name"); err == nil {
		t.Fatal("expected error for path separator")
	}
}

func TestSaveSessionNamedDoesNotMutateRollingSessionID(t *testing.T) {
	rt := &agent.Runtime{
		Policy: workspace.Policy{Root: t.TempDir()},
		Session: &session.Session{
			ID:       "rolling",
			Provider: "p",
			Model:    "m",
		},
	}
	if _, err := saveSessionNamed(rt, config.Selection{Provider: "p", Model: "m"}, "snapshot"); err != nil {
		t.Fatal(err)
	}
	if rt.Session.ID != "rolling" {
		t.Fatalf("session id mutated: %q", rt.Session.ID)
	}
}

func TestLoginArgsProvider(t *testing.T) {
	provider := loginArgsProvider([]string{"--provider", "openai"})
	if provider != "openai" {
		t.Fatalf("provider=%q", provider)
	}
	provider = loginArgsProvider([]string{"openai-codex"})
	if provider != "openai-codex" {
		t.Fatalf("provider=%q", provider)
	}
	provider = loginArgsProvider([]string{"--api-key", "secret", "--base-url=https://example.invalid"})
	if provider != "" {
		t.Fatalf("provider=%q", provider)
	}
	provider = loginArgsProvider([]string{"chatgpt"})
	if provider != "openai-codex" {
		t.Fatalf("provider=%q", provider)
	}
}

func TestInteractiveProviderAllowsMissingProvider(t *testing.T) {
	p, notice, err := interactiveProvider(config.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatal("expected nil provider")
	}
	if notice == "" {
		t.Fatal("expected startup notice")
	}
}

func TestChatLoginFlowHelpers(t *testing.T) {
	kind, args := stripLoginKindArg([]string{"sub", "openai", "--model", "gpt-5.3-codex"})
	if kind != "sub" || len(args) != 3 || args[0] != "openai" {
		t.Fatalf("kind=%q args=%#v", kind, args)
	}
	provider, err := providerForLoginKind("sub", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if provider != "openai-codex" {
		t.Fatalf("provider = %q", provider)
	}
	if _, err := providerForLoginKind("sub", "opencode-go"); err == nil {
		t.Fatal("expected sub provider restriction")
	}
	if got := loginProviderNames(&config.Config{}, "sub"); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("sub providers = %#v", got)
	}
	apiProviders := loginProviderNames(&config.Config{}, "api")
	for _, want := range []string{"openai", "openrouter", "groq", "deepseek", "together", "fireworks", "perplexity", "mistral", "xai", "cerebras", "sambanova", "opencode-go"} {
		found := false
		for _, got := range apiProviders {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("api providers missing %q: %#v", want, apiProviders)
		}
	}
	got := withLoginProviderArg([]string{"--api-key", "k"}, "openai-codex")
	if len(got) != 3 || got[2] != "openai-codex" {
		t.Fatalf("args = %#v", got)
	}
}

func TestPromptLoginProviderChoiceShowsConfiguredMarker(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Providers["openrouter"] = config.ProviderConfig{BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAuth(&config.Auth{Keys: map[string]string{"openrouter": "secret"}}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(strings.NewReader("\n"))
	var out strings.Builder
	choice, err := promptLoginProviderChoice(reader, &out, cfg, "api", "")
	if err != nil {
		t.Fatal(err)
	}
	if choice != "" {
		t.Fatalf("choice = %q", choice)
	}
	text := out.String()
	if !strings.Contains(text, "openrouter [configured]") {
		t.Fatalf("output missing configured marker: %q", text)
	}
	if strings.Contains(text, "OpenRouter API key") || strings.Contains(text, "known profile") {
		t.Fatalf("output too verbose: %q", text)
	}
}

func TestResolveReadyModelChoiceFromList(t *testing.T) {
	choices := []readyModelChoice{
		{Provider: "p1", Model: provider.Model{ID: "gpt-5-codex"}},
		{Provider: "p2", Model: provider.Model{ID: "gpt-4.1"}},
	}
	got, err := resolveReadyModelChoiceFromList(choices, "2", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.ID != "gpt-4.1" || got.Provider != "p2" {
		t.Fatalf("got = %+v", got)
	}
	got, err = resolveReadyModelChoiceFromList(choices, "GPT-5-CODEX", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.ID != "gpt-5-codex" {
		t.Fatalf("got = %+v", got)
	}
}

func TestResolveReadyModelChoicePrefersCurrentProvider(t *testing.T) {
	choices := []readyModelChoice{
		{Provider: "p1", Model: provider.Model{ID: "same"}},
		{Provider: "p2", Model: provider.Model{ID: "same"}},
	}
	got, err := resolveReadyModelChoiceFromList(choices, "same", "p2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "p2" {
		t.Fatalf("got = %+v", got)
	}
}

func TestChatLoginReaderSkipsStdinForBrowserLogin(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("hello\n"))
	if got := chatLoginReader("openai-codex", reader); got == reader {
		t.Fatal("expected replacement reader for browser login")
	}
	if got := chatLoginReader("openai", reader); got != reader {
		t.Fatal("expected original reader for api login")
	}
}

func TestSelectionHasReadyModel(t *testing.T) {
	choices := []readyModelChoice{{Provider: "p1", Model: provider.Model{ID: "m1"}}}
	if !selectionHasReadyModel(config.Selection{Provider: "p1", Model: "m1"}, choices) {
		t.Fatal("expected ready model match")
	}
	if selectionHasReadyModel(config.Selection{Provider: "p1", Model: "m2"}, choices) {
		t.Fatal("unexpected ready model match")
	}
}

func TestParseToolVerbosity(t *testing.T) {
	got, err := parseToolVerbosity("on")
	if err != nil || got != toolVerbosityOn {
		t.Fatalf("got=%q err=%v", got, err)
	}
	got, err = parseToolVerbosity("")
	if err != nil || got != toolVerbosityErrors {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := parseToolVerbosity("loud"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCleanDisplayText(t *testing.T) {
	got := cleanDisplayText("IÔÇÖm **oi**\n## Title\n`code`")
	want := "I’m oi\nTitle\ncode"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatContextUsage(t *testing.T) {
	got := formatContextUsage(272000, provider.Usage{InputTokens: 136000})
	if got != "ctx 136.0k / 272.0k (50%)" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectedModelStartupNotice(t *testing.T) {
	p := &fakeChatProvider{models: []provider.Model{{ID: "m1"}}}
	if got := selectedModelStartupNotice(p, "m1"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := selectedModelStartupNotice(p, "m2"); got != "Selected model m2 is unavailable. Use /model." {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSessionArgByIndexAndFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	for _, s := range []*session.Session{
		{ID: "alpha", Provider: "p1", Model: "m1", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "beta", Provider: "p2", Model: "deepseek", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	} {
		if _, err := session.Save(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := filteredSessions(dir, "deep")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != "beta" {
		t.Fatalf("infos = %+v", infos)
	}
	all, err := filteredSessions(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := resolveSessionArg(dir, all, "1")
	if err != nil {
		t.Fatal(err)
	}
	if path != all[0].Path {
		t.Fatalf("path = %q want %q", path, all[0].Path)
	}
}

type clearRecorder struct{ called bool }

func (c *clearRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (c *clearRecorder) ClearScreen()                { c.called = true }

func TestHandleChatCommandClear(t *testing.T) {
	out := &clearRecorder{}
	_, _, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, nil, bufio.NewReader(strings.NewReader("")), out, "/clear", true, true, toolVerbosityErrors)
	if err != nil {
		t.Fatal(err)
	}
	if !out.called {
		t.Fatal("expected clear screen call")
	}
}

func TestHandleChatCommandCompact(t *testing.T) {
	var out strings.Builder
	rt := &agent.Runtime{Provider: &fakeChatProvider{}, Policy: workspace.Policy{Root: t.TempDir()}, Session: session.New("fake", "m", "."), ContextWindow: 100}
	rt.Session.Messages = append(rt.Session.Messages, session.Message{Role: "user", Kind: "talk", Content: "hello there"})
	_, nextRT, _, _, _, _, err := handleChatCommand(Dependencies{}, config.Default(), config.Selection{}, rt, bufio.NewReader(strings.NewReader("")), &out, "/compact", true, false, toolVerbosityErrors)
	if err != nil {
		t.Fatal(err)
	}
	if len(nextRT.Session.Messages) != 1 || nextRT.Session.Messages[0].Kind != "summary" {
		t.Fatalf("session messages = %+v", nextRT.Session.Messages)
	}
	if !strings.Contains(out.String(), "session compacted") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestWrapPromptLines(t *testing.T) {
	lines := wrapPromptLines("oi> ", "abcdefghi", 8)
	if len(lines) < 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "oi> abcd" {
		t.Fatalf("first = %q", lines[0])
	}
}

func TestWrapLinePrefersWordBoundaries(t *testing.T) {
	lines := wrapLine("alpha beta gamma", 7)
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "alpha" || lines[1] != "beta" || lines[2] != "gamma" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestNormalizePastedText(t *testing.T) {
	got := normalizePastedText("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatToolStartLine(t *testing.T) {
	call := tool.Call{Name: "read_file", Args: []byte(`{"path":"internal/chat/ui.go"}`)}
	if got := formatToolStartLine(call); got != "  |> read internal/chat/ui.go" {
		t.Fatalf("got %q", got)
	}
	if got := toolActivityLabel(call); got != "reading internal/chat/ui.go" {
		t.Fatalf("activity = %q", got)
	}
}

func TestFormatToolResultLine(t *testing.T) {
	call := tool.Call{Name: "grep", Args: []byte(`{"pattern":"RunOnce","path":"internal/agent"}`)}
	ok := tool.Result{Tool: "grep", OK: true}
	if got := formatToolResultLine(call, ok); got != "  |  grep ok \"RunOnce\" in internal/agent" {
		t.Fatalf("ok = %q", got)
	}
	bad := tool.Result{Tool: "grep", OK: false, Error: "permission denied"}
	if got := formatToolResultLine(call, bad); got != "  !  grep failed \"RunOnce\" in internal/agent: permission denied" {
		t.Fatalf("bad = %q", got)
	}
}

func TestTrailingToken(t *testing.T) {
	start, end, token, ok := trailingToken("read @chat/ui")
	if !ok || token != "@chat/ui" || start != 5 || end != 13 {
		t.Fatalf("start=%d end=%d token=%q ok=%v", start, end, token, ok)
	}
}

func TestFuzzyFileMatchesPrefersBasename(t *testing.T) {
	matches := fuzzyFileMatches("ui.go", []string{"internal/chat/ui.go", "internal/provider/openai.go"}, 8)
	if len(matches) == 0 || matches[0] != "internal/chat/ui.go" {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestFormatCompletionMatches(t *testing.T) {
	got := formatCompletionMatches([]string{"a.go", "b.go", "c.go"})
	if !strings.Contains(got, "matches:") || !strings.Contains(got, "1. a.go") || !strings.Contains(got, "3. c.go") {
		t.Fatalf("got = %q", got)
	}
}

func TestLiveHint(t *testing.T) {
	if got := liveHint(nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := liveHint([]string{"a.go"}); !strings.Contains(got, "a.go") {
		t.Fatalf("one = %q", got)
	}
	if got := liveHint([]string{"a.go", "b.go"}); !strings.Contains(got, "2 matches") {
		t.Fatalf("multi = %q", got)
	}
}

func TestPromptHistoryNavigation(t *testing.T) {
	ui := &terminalUI{historyIndex: -1}
	ui.addHistoryEntry("first")
	ui.addHistoryEntry("second")
	got, ok := ui.historyPrev("draft")
	if !ok || got != "second" {
		t.Fatalf("prev1 = %q ok=%v", got, ok)
	}
	got, ok = ui.historyPrev("draft")
	if !ok || got != "first" {
		t.Fatalf("prev2 = %q ok=%v", got, ok)
	}
	got, ok = ui.historyNext()
	if !ok || got != "second" {
		t.Fatalf("next1 = %q ok=%v", got, ok)
	}
	got, ok = ui.historyNext()
	if !ok || got != "draft" {
		t.Fatalf("next2 = %q ok=%v", got, ok)
	}
}
