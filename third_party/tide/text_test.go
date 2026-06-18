package tide

import "testing"

func TestWrapPrefersWordBoundary(t *testing.T) {
	lines := Wrap("hello world", 8)
	if len(lines) != 2 || lines[0] != "hello" || lines[1] != "world" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestWrapHardWrapsLongWord(t *testing.T) {
	lines := Wrap("superlong", 5)
	if len(lines) != 2 || lines[0] != "super" || lines[1] != "long" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestDisplayWidthHandlesWideRunes(t *testing.T) {
	if w := DisplayWidth("界界界"); w != 6 {
		t.Fatalf("width = %d, want 6", w)
	}
	if w := DisplayWidth("ab"); w != 2 {
		t.Fatalf("width = %d, want 2", w)
	}
	if w := DisplayWidth(""); w != 0 {
		t.Fatalf("width = %d, want 0", w)
	}
}

func TestWrapPlainSplitsParagraphs(t *testing.T) {
	out := WrapPlain("hello\nworld", 80)
	if len(out) != 2 || out[0] != "hello" || out[1] != "world" {
		t.Fatalf("out = %#v", out)
	}
}

func TestWriteClippedTruncatesToWidth(t *testing.T) {
	var b simpleWriter
	WriteClipped(&b, 1, 1, 5, "hello world")
	if b.s != "hello" {
		t.Fatalf("got %q", b.s)
	}
}

type simpleWriter struct{ s string }

func (w *simpleWriter) Write(p []byte) (int, error) {
	w.s = string(p)
	return len(p), nil
}
