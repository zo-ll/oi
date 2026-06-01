package chat

import (
	"bufio"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
)

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

func TestLoginArgsSelection(t *testing.T) {
	provider, model := loginArgsSelection([]string{"--provider", "openai", "--model=gpt-test"})
	if provider != "openai" || model != "gpt-test" {
		t.Fatalf("provider=%q model=%q", provider, model)
	}
	provider, model = loginArgsSelection([]string{"openai-codex", "--model", "gpt-5.3-codex"})
	if provider != "openai-codex" || model != "gpt-5.3-codex" {
		t.Fatalf("provider=%q model=%q", provider, model)
	}
	provider, model = loginArgsSelection([]string{"--api-key", "secret", "--base-url=https://example.invalid", "--model", "m"})
	if provider != "" || model != "m" {
		t.Fatalf("provider=%q model=%q", provider, model)
	}
	provider, model = loginArgsSelection([]string{"chatgpt"})
	if provider != "openai-codex" || model != "" {
		t.Fatalf("provider=%q model=%q", provider, model)
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
	got := withLoginProviderArg([]string{"--model", "m"}, "openai-codex")
	if len(got) != 3 || got[2] != "openai-codex" {
		t.Fatalf("args = %#v", got)
	}
	got = ensureLoginDefaultArg(got)
	if got[len(got)-1] != "--default" {
		t.Fatalf("default args = %#v", got)
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

func TestWrapPromptLines(t *testing.T) {
	lines := wrapPromptLines("oi> ", "abcdefghi", 8)
	if len(lines) < 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "oi> abcd" {
		t.Fatalf("first = %q", lines[0])
	}
}

func TestNormalizePastedText(t *testing.T) {
	got := normalizePastedText("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}
