package chat

import (
	"io"
	"testing"
)

func TestNextByteCollapsesCRLF(t *testing.T) {
	a := &tuiApp{inputCh: make(chan byte, 4)}
	a.inputCh <- 'a'
	a.inputCh <- '\r'
	a.inputCh <- '\n'
	a.inputCh <- 'b'

	got, err := a.nextByte()
	if err != nil || got != 'a' {
		t.Fatalf("first = %q err %v", got, err)
	}
	got, err = a.nextByte()
	if err != nil || got != '\r' {
		t.Fatalf("cr = %q err %v", got, err)
	}
	// The LF following CR must be skipped.
	got, err = a.nextByte()
	if err != nil || got != 'b' {
		t.Fatalf("after crlf = %q err %v", got, err)
	}
}

func TestNextByteKeepsBareLF(t *testing.T) {
	a := &tuiApp{inputCh: make(chan byte, 2)}
	a.inputCh <- 'x'
	a.inputCh <- '\n'
	got, err := a.nextByte()
	if err != nil || got != 'x' {
		t.Fatalf("first = %q err %v", got, err)
	}
	got, err = a.nextByte()
	if err != nil || got != '\n' {
		t.Fatalf("lf = %q err %v", got, err)
	}
}

func TestNextByteWithQuitCtrlC(t *testing.T) {
	a := &tuiApp{inputCh: make(chan byte, 1)}
	a.inputCh <- 3
	_, err := a.nextByteWithQuit()
	if err == nil {
		t.Fatal("expected EOF on Ctrl-C")
	}
}

func TestNextByteWithQuitCtrlD(t *testing.T) {
	a := &tuiApp{inputCh: make(chan byte, 1)}
	a.inputCh <- 4
	_, err := a.nextByteWithQuit()
	if err == nil {
		t.Fatal("expected EOF on Ctrl-D")
	}
}

func TestNextByteWithQuitPassesNormal(t *testing.T) {
	a := &tuiApp{inputCh: make(chan byte, 1)}
	a.inputCh <- 'z'
	b, err := a.nextByteWithQuit()
	if err != nil || b != 'z' {
		t.Fatalf("got %q err %v", b, err)
	}
	if a.quitRequested {
		t.Fatal("quitRequested should not be set")
	}
}

func TestNextByteWithQuitPassesEOF(t *testing.T) {
	a := &tuiApp{errCh: make(chan error, 1)}
	a.errCh <- io.EOF
	_, err := a.nextByteWithQuit()
	if err != io.EOF {
		t.Fatalf("got %v want EOF", err)
	}
	if a.quitRequested {
		t.Fatal("quitRequested should not be set for raw EOF")
	}
}
