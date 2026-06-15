package chat

import (
	"bufio"
	"context"
	"io"
	"os"
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

func TestNewSessionThinkingDefaultsOff(t *testing.T) {
	s := session.New("openai", "gpt", ".")
	if s.ThinkingLevel != "off" {
		t.Fatalf("thinking = %q", s.ThinkingLevel)
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

func TestFormatHeaderShowsValuesOnly(t *testing.T) {
	got := formatHeader("gpt5.5", "/tmp/project", 272000, provider.Usage{InputTokens: 136000}, "high", true)
	if got != "oi · gpt5.5 · high · 136.0k / 272.0k (50%) · /tmp/project" {
		t.Fatalf("got %q", got)
	}
}

func TestFilterPickerItems(t *testing.T) {
	got := filterPickerItems([]string{"alpha", "beta", "gamma"}, "et")
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("got = %#v", got)
	}
}

func TestOverlayPickerFiltersTypedInput(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close()
	out, err := os.CreateTemp(t.TempDir(), "picker-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: inR, out: out, width: 80}
	go func() {
		_, _ = inW.Write([]byte{'b', 'e', '\n'})
	}()
	selected, ok := ui.overlayPicker("choose", []string{"alpha", "beta", "gamma"})
	if !ok || selected != "beta" {
		t.Fatalf("selected=%q ok=%v", selected, ok)
	}
}

func TestOverlayPickerHandlesShortItemLists(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close()
	out, err := os.CreateTemp(t.TempDir(), "picker-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: inR, out: out, width: 80}
	go func() {
		_, _ = inW.Write([]byte{27})
		_ = inW.Close()
	}()
	selected, ok := ui.overlayPicker("choose login type", []string{
		"sub  ChatGPT subscription / browser login",
		"api  Provider API key",
	})
	if ok || selected != "" {
		t.Fatalf("selected=%q ok=%v", selected, ok)
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

func TestPromptCursorPositionWraps(t *testing.T) {
	row, col := promptCursorPosition("oi> ", "abcdefghi", 8, 6)
	if row != 1 || col != 6 {
		t.Fatalf("row=%d col=%d", row, col)
	}
}

func TestReadMessageEditsAtCursor(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close()
	out, err := os.CreateTemp(t.TempDir(), "prompt-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: inR, out: out, width: 80, prompt: "> ", historyIndex: -1}
	go func() {
		_, _ = inW.Write([]byte{'a', 'b', 'c', 27, '[', 'D', 27, '[', 'D', 'X', '\n'})
	}()
	got, err := ui.readMessage("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "aXbc" {
		t.Fatalf("got %q", got)
	}
}

func TestReadMessageHomeEndDelete(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inR.Close()
	defer inW.Close()
	out, err := os.CreateTemp(t.TempDir(), "prompt-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: inR, out: out, width: 80, prompt: "> ", historyIndex: -1}
	go func() {
		_, _ = inW.Write([]byte{'a', 'b', 'c', 27, '[', 'H', 'X', 27, '[', 'F', 'Y', 27, '[', 'H', 27, '[', '3', '~', '\n'})
	}()
	got, err := ui.readMessage("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcY" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalWriteWrappedDoesNotSplitWords(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 9}
	ui.writeWrapped("alpha beta")
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\r\nbeta\r\n" {
		t.Fatalf("out = %q", string(data))
	}
}

func TestTerminalWriteWrappedKeepsWordsAcrossChunks(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 9}
	ui.writeWrapped("feed")
	ui.writeWrapped("back now")
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "feed\r\nback") || !strings.Contains(string(data), "feedback") {
		t.Fatalf("out = %q", string(data))
	}
}

func TestTerminalWriteWrappedAfterExactFit(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 10}
	ui.writeWrapped("alpha beta gamma")
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha beta\r\ngamma\r\n" {
		t.Fatalf("out = %q", string(data))
	}
}

func TestTerminalWriteWrappedDoesNotSplitLongWords(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 5}
	ui.writeWrapped("supercalifragilistic")
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSuffix(string(data), "\r\n")
	if strings.Contains(text, "\r\n") {
		t.Fatalf("word was split: %q", string(data))
	}
}

func TestTerminalWriteWrappedJoinsSplitStreamingWord(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 80}
	ui.writeWrapped("feed")
	ui.writeWrapped("back ")
	ui.writeWrapped("works\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "feedback works\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalWriteWrappedStreamingWordBoundary(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 10}
	for _, chunk := range []string{"alpha", " beta", " gam", "ma\n"} {
		ui.writeWrapped(chunk)
	}
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "alpha beta\r\ngamma\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTaggedStreamRendererSplitWord(t *testing.T) {
	r := &taggedStreamRenderer{}
	var b strings.Builder
	for _, chunk := range []string{"feed", "back "} {
		for _, seg := range r.Push(chunk) {
			b.WriteString(seg.text)
		}
	}
	for _, seg := range r.Flush() {
		b.WriteString(seg.text)
	}
	if got := b.String(); got != "feedback " {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalWriteWrappedExactWidthLines(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 5}
	ui.writeWrapped("alpha beta\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "alpha\r\nbeta\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamRendererPreservesTextAcrossChunks(t *testing.T) {
	text := "Here is my opinion. Let me also peek. Here is my take."
	var chunks []string
	runes := []rune(text)
	for i := 0; i < len(runes); {
		n := 3
		if i+n > len(runes) {
			n = len(runes) - i
		}
		chunks = append(chunks, string(runes[i:i+n]))
		i += n
	}
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 80}
	ui.startAssistantResponse()
	r := &taggedStreamRenderer{}
	for _, c := range chunks {
		for _, seg := range r.Push(c) {
			ui.writeResponseSegment(seg)
		}
	}
	for _, seg := range r.Flush() {
		ui.writeResponseSegment(seg)
	}
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	want := text + "\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStreamRendererPreservesMultilineMarkdown(t *testing.T) {
	text := "Brief take:\n\n- It's oi, a small agent.\n- Design is clean.\n\nOverall: solid."
	var chunks []string
	runes := []rune(text)
	for i := 0; i < len(runes); {
		n := 5
		if i+n > len(runes) {
			n = len(runes) - i
		}
		chunks = append(chunks, string(runes[i:i+n]))
		i += n
	}
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 80}
	ui.startAssistantResponse()
	r := &taggedStreamRenderer{}
	for _, c := range chunks {
		for _, seg := range r.Push(c) {
			ui.writeResponseSegment(seg)
		}
	}
	for _, seg := range r.Flush() {
		ui.writeResponseSegment(seg)
	}
	ui.writeWrapped("\n")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	want := text + "\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTerminalResponseSegmentsSeparated(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 80}
	ui.startAssistantResponse()
	ui.writeResponseSegment(responseSegment{text: "plan ", reasoning: true})
	ui.writeResponseSegment(responseSegment{text: "answer\n"})
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "answer\r\n") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "response") {
		t.Fatalf("unexpected response label: %q", got)
	}
}

func TestCommitInputLeavesGapBeforeResponse(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "term-out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ui := &terminalUI{in: out, out: out, width: 80, prompt: "> "}
	ui.commitInput("hello")
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "> hello\r\n\r\n" {
		t.Fatalf("got %q", got)
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

func TestCompletionMatchesForBareAt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := &terminalUI{historyIndex: -1}
	ui.setWorkspaceRoot(root)
	matches, err := ui.completionMatchesForText("@")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected matches for bare @")
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

func TestChatCommands(t *testing.T) {
	cmds := chatCommands()
	for _, want := range []string{"/help", "/model", "/login", "/session", "/exit"} {
		found := false
		for _, got := range cmds {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestFilterByPrefix(t *testing.T) {
	got := filterByPrefix([]string{"/model", "/help", "/login"}, "mo")
	if len(got) != 1 || got[0] != "/model" {
		t.Fatalf("got = %#v", got)
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
	for _, line := range []string{"/help now", "/login api", "/model gpt", "/stream off", "/think high", "/tools on", "/autosave off", "/save snap", "/new x", "/clear now", "/exit now"} {
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
