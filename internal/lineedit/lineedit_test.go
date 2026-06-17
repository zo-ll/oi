package lineedit

import (
	"strings"
	"testing"
)

func TestWrapPromptLines(t *testing.T) {
	lines := wrapPromptLines("oi> ", "abcdefghi", 8)
	if len(lines) != 3 {
		t.Fatalf("lines = %#v", lines)
	}
	if lines[0] != "oi> abcd" || lines[1] != "    efgh" || lines[2] != "    i" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestPromptCursorPositionWraps(t *testing.T) {
	row, col := promptCursorPosition("oi> ", "abcdefghi", 8, 6)
	if row != 1 || col != 6 {
		t.Fatalf("row=%d col=%d", row, col)
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
