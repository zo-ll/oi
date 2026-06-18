package tide

import (
	"strings"
	"testing"
)

func TestWrapPromptLines(t *testing.T) {
	lines := wrapPromptLines("oi> ", "abcdefghi", 8)
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "oi> abc" || lines[1] != "    def" || lines[2] != "    ghi" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestPromptCursorPositionWraps(t *testing.T) {
	row, col := promptCursorPosition("oi> ", "abcdefghi", 8, 6)
	if row != 1 || col != 7 {
		t.Fatalf("row=%d col=%d", row, col)
	}
}

func TestWrapPromptLinesUsesDisplayWidth(t *testing.T) {
	lines := wrapPromptLines("oi> ", "界界界", 8)
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "oi> 界" || lines[1] != "    界" || lines[2] != "    界" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestWrapLinePrefersWordBoundary(t *testing.T) {
	lines := wrapLine("hello world", 8)
	if len(lines) != 2 || lines[0] != "hello" || lines[1] != "world" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestWrapLineHardWrapsLongWord(t *testing.T) {
	lines := wrapLine("superlong", 5)
	if len(lines) != 3 || lines[0] != "supe" || lines[1] != "rlon" || lines[2] != "g" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestPromptCursorPositionUsesDisplayWidth(t *testing.T) {
	row, col := promptCursorPosition("oi> ", "界界界", 8, 2)
	if row != 1 || col != 6 {
		t.Fatalf("row=%d col=%d", row, col)
	}
}

func TestWrapPromptLinesAvoidsTerminalEdge(t *testing.T) {
	lines := wrapPromptLines("", "12345678", 8)
	if lines[0] != "1234567" || lines[1] != "8" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestWordMovement(t *testing.T) {
	buf := []rune("one two  three")
	if got := wordLeft(buf, len(buf)); got != 9 {
		t.Fatalf("wordLeft end = %d", got)
	}
	if got := wordRight(buf, 0); got != 4 {
		t.Fatalf("wordRight start = %d", got)
	}
}

func TestStatusRerendersPrompt(t *testing.T) {
	var out strings.Builder
	e := &Editor{out: &out, prompt: "> ", width: 20}
	e.render([]rune("hello"), 5)
	e.ShowStatus("thinking")
	got := out.String()
	if !strings.Contains(got, dim("thinking")) || !strings.Contains(got, "> hello") {
		t.Fatalf("render = %q", got)
	}
}

func TestStandaloneStatusClearsCurrentLine(t *testing.T) {
	var out strings.Builder
	e := &Editor{out: &out, width: 20}
	e.ShowStatus("thinking /")
	e.ShowStatus("thinking -")
	got := out.String()
	if strings.Contains(got, "\x1b[1A") {
		t.Fatalf("standalone status should clear current line, got %q", got)
	}
	if !strings.Contains(got, "\r\x1b[J") {
		t.Fatalf("standalone status should clear to end, got %q", got)
	}
}

func TestWriteWrapsAtWordBoundary(t *testing.T) {
	var out strings.Builder
	e := &Editor{out: &out, width: 8}
	if _, err := e.Write([]byte("hello world\n")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hello \nworld\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteWrapsStyledWordsByVisibleWidth(t *testing.T) {
	var out strings.Builder
	e := &Editor{out: &out, width: 8}
	if _, err := e.Write([]byte(dim("hello world") + "\n")); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "\n") || strings.Contains(got, "wor\nld") {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizePaste(t *testing.T) {
	got := normalizePaste("a\r\nb\rc")
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestHistoryNavigation(t *testing.T) {
	e := &Editor{historyIndex: -1}
	e.addHistory("one")
	e.addHistory("two")
	if got, ok := e.historyPrev("draft"); !ok || got != "two" {
		t.Fatalf("prev = %q %v", got, ok)
	}
	if got, ok := e.historyPrev("unused"); !ok || got != "one" {
		t.Fatalf("prev = %q %v", got, ok)
	}
	if _, ok := e.historyPrev("unused"); ok {
		t.Fatalf("unexpected prev at start")
	}
	if got, ok := e.historyNext(); !ok || got != "two" {
		t.Fatalf("next = %q %v", got, ok)
	}
	if got, ok := e.historyNext(); !ok || got != "draft" {
		t.Fatalf("draft = %q %v", got, ok)
	}
}

func TestClearRowsAccountsForTrailingNewline(t *testing.T) {
	var out strings.Builder
	clearRows(&out, 3)
	got := out.String()
	if !strings.Contains(got, "\x1b[3A") {
		t.Fatalf("clearRows should move up over all rendered rows, got %q", got)
	}
}
