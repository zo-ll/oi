package tide

import (
	"errors"
	"strings"
	"testing"
)

// fakeInput is a byte source for widget tests: it hands out a scripted byte
// sequence via Next, mimicking a terminal input stream.
type fakeInput struct {
	bytes []byte
	i     int
}

func (f *fakeInput) Next() (byte, error) {
	if f.i >= len(f.bytes) {
		return 0, errEOF
	}
	b := f.bytes[f.i]
	f.i++
	return b, nil
}

var errEOF = errors.New("end of scripted input")

func newOverlay(next *fakeInput) Overlay {
	return Overlay{
		Out:  &strings.Builder{},
		Size: func() Size { return Size{Rows: 24, Cols: 80} },
		Base: func() {}, // no-op repaint
		Next: next.Next,
	}
}

func TestPickerSelectsFirstOnEnter(t *testing.T) {
	in := &fakeInput{bytes: []byte{'\r'}}
	sel, ok := NewPicker(newOverlay(in)).Open("pick", []string{"one", "two", "three"})
	if !ok || sel != "one" {
		t.Fatalf("sel = %q ok=%v", sel, ok)
	}
}

func TestPickerArrowDownThenEnter(t *testing.T) {
	// ESC [ B = down, then enter
	in := &fakeInput{bytes: []byte{27, '[', 'B', '\r'}}
	sel, ok := NewPicker(newOverlay(in)).Open("pick", []string{"one", "two", "three"})
	if !ok || sel != "two" {
		t.Fatalf("sel = %q ok=%v", sel, ok)
	}
}

func TestPickerCancelOnEscape(t *testing.T) {
	// bare ESC -> ReadEscape sees no '[' follow-up -> EscCancel -> cancel
	in := &fakeInput{bytes: []byte{27, 'x'}}
	sel, ok := NewPicker(newOverlay(in)).Open("pick", []string{"one", "two"})
	if ok || sel != "" {
		t.Fatalf("sel = %q ok=%v, want cancel", sel, ok)
	}
}

func TestPickerFilterThenEnter(t *testing.T) {
	// type "th" filters to "three", enter selects it
	in := &fakeInput{bytes: []byte("th\r")}
	sel, ok := NewPicker(newOverlay(in)).Open("pick", []string{"one", "two", "three"})
	if !ok || sel != "three" {
		t.Fatalf("sel = %q ok=%v", sel, ok)
	}
}

func TestPickerEmptyReturnsFalse(t *testing.T) {
	sel, ok := NewPicker(newOverlay(&fakeInput{})).Open("pick", nil)
	if ok || sel != "" {
		t.Fatalf("sel = %q ok=%v, want false", sel, ok)
	}
}

func TestPickerBackspaceClearsFilter(t *testing.T) {
	// type "o" (matches one,two), backspace, enter -> first of full list = one
	in := &fakeInput{bytes: []byte{'o', 127, '\r'}}
	sel, ok := NewPicker(newOverlay(in)).Open("pick", []string{"one", "two"})
	if !ok || sel != "one" {
		t.Fatalf("sel = %q ok=%v", sel, ok)
	}
}

func TestPromptReturnsTextOnEnter(t *testing.T) {
	in := &fakeInput{bytes: []byte("hi\r")}
	text, ok := NewPrompt(newOverlay(in)).Open("save", "name: ", "")
	if !ok || text != "hi" {
		t.Fatalf("text = %q ok=%v", text, ok)
	}
}

func TestPromptInitialPrefilled(t *testing.T) {
	in := &fakeInput{bytes: []byte{'\r'}}
	text, ok := NewPrompt(newOverlay(in)).Open("save", "name: ", "default")
	if !ok || text != "default" {
		t.Fatalf("text = %q ok=%v", text, ok)
	}
}

func TestPromptCancelOnEscape(t *testing.T) {
	in := &fakeInput{bytes: []byte{27, 'x'}}
	text, ok := NewPrompt(newOverlay(in)).Open("save", "name: ", "")
	if ok || text != "" {
		t.Fatalf("text = %q ok=%v, want cancel", text, ok)
	}
}

func TestPromptBackspaceDeletes(t *testing.T) {
	// type "abc", backspace, enter -> "ab"
	in := &fakeInput{bytes: []byte{'a', 'b', 'c', 127, '\r'}}
	text, ok := NewPrompt(newOverlay(in)).Open("save", "name: ", "")
	if !ok || text != "ab" {
		t.Fatalf("text = %q ok=%v", text, ok)
	}
}

func TestPromptArrowLeftThenBackspace(t *testing.T) {
	// type "abc", left, backspace -> deletes 'b' -> "ac"
	in := &fakeInput{bytes: []byte{'a', 'b', 'c', 27, '[', 'D', 127, '\r'}}
	text, ok := NewPrompt(newOverlay(in)).Open("save", "name: ", "")
	if !ok || text != "ac" {
		t.Fatalf("text = %q ok=%v", text, ok)
	}
}

func TestDrawBoxEmitsBorders(t *testing.T) {
	var b strings.Builder
	DrawBox(&b, 1, 1, 10, 5, "title")
	got := b.String()
	if !strings.Contains(got, "+--------+") {
		t.Fatalf("missing top border: %q", got)
	}
	if !strings.Contains(got, "| title") {
		t.Fatalf("missing title: %q", got)
	}
}

func TestViewportScrollAndClamp(t *testing.T) {
	v := &Viewport{Height: 3}
	v.SetLines([]string{"a", "b", "c", "d", "e"})
	if got := v.Visible(); len(got) != 3 || got[0] != "a" {
		t.Fatalf("visible = %#v", got)
	}
	v.Scroll(1)
	if got := v.Visible(); got[0] != "b" {
		t.Fatalf("after scroll visible = %#v", got)
	}
	v.Scroll(100) // clamp
	if got := v.Visible(); got[len(got)-1] != "e" {
		t.Fatalf("after over-scroll visible = %#v", got)
	}
	v.Scroll(-100) // clamp to top
	if got := v.Visible(); got[0] != "a" {
		t.Fatalf("after under-scroll visible = %#v", got)
	}
}

func TestViewportBottom(t *testing.T) {
	v := &Viewport{Height: 2}
	v.SetLines([]string{"a", "b", "c", "d"})
	v.Bottom()
	if got := v.Visible(); len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("bottom visible = %#v", got)
	}
}

func TestHeaderLinePadsTwoColumns(t *testing.T) {
	var b strings.Builder
	HeaderLine(&b, "left", "right", 20, 1, 1)
	got := b.String()
	if !strings.Contains(got, "left") || !strings.Contains(got, "right") {
		t.Fatalf("missing columns: %q", got)
	}
}

func TestHeaderLineDropsRightWhenNoRoom(t *testing.T) {
	var b strings.Builder
	HeaderLine(&b, "a very long left side", "r", 10, 1, 1)
	got := b.String()
	// width 10, left is 21 wide -> clipped to ~8 cells; right won't fit
	if strings.Contains(got, "r") && strings.Contains(got, "right") {
		t.Fatalf("right should not render when no room: %q", got)
	}
}
