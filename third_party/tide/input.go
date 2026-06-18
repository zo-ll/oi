package tide

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Input reads bytes from a terminal file in a goroutine and delivers them over
// a channel. It is the channel-friendly counterpart to the blocking readByte
// used by Editor: callers that need to select on input alongside other event
// sources (an async TUI loop, approval channels, model events) consume
// Input.Next() instead of blocking on a file read.
type Input struct {
	file *os.File
	ch   chan byte
	err  error
	once sync.Once
}

// NewInput starts a goroutine that copies bytes from f into an internal
// channel. Next returns the next byte, or an error once the read fails.
func NewInput(f *os.File) *Input {
	in := &Input{file: f, ch: make(chan byte, 128)}
	go in.pump()
	return in
}

func (in *Input) pump() {
	var buf [1]byte
	for {
		n, err := in.file.Read(buf[:])
		if n > 0 {
			in.ch <- buf[0]
		}
		if err != nil {
			in.err = err
			close(in.ch)
			return
		}
	}
}

// Next returns the next byte. Once the underlying read fails, Next returns
// (0, err) on every subsequent call.
func (in *Input) Next() (byte, error) {
	b, ok := <-in.ch
	if !ok {
		return 0, io.EOF
	}
	return b, nil
}

// Err returns the last read error, if any.
func (in *Input) Err() error { return in.err }

// Stop signals the pump goroutine to exit by closing the file's read side.
// It is safe to call multiple times.
func (in *Input) Stop() {
	in.once.Do(func() { _ = in.file.Close() })
}

// EscapeKind is the normalized name of a terminal escape/mouse sequence.
type EscapeKind string

const (
	EscUp       EscapeKind = "up"
	EscDown     EscapeKind = "down"
	EscLeft     EscapeKind = "left"
	EscRight    EscapeKind = "right"
	EscHome     EscapeKind = "home"
	EscEnd      EscapeKind = "end"
	EscPageUp   EscapeKind = "page-up"
	EscPageDown EscapeKind = "page-down"
	EscScrollUp EscapeKind = "scroll-up"
	EscScrollDn EscapeKind = "scroll-down"
	EscMouse    EscapeKind = "mouse"
	EscCancel   EscapeKind = "cancel"
)

// ReadEscape consumes a full escape sequence following an ESC (0x1b) byte that
// has already been read. next is the byte source (typically Input.Next or a
// wrapper that also pumps other event channels). It returns the normalized
// kind and, for mouse sequences, the raw text. For a plain ESC with no
// bracketed follow-up it returns EscCancel so callers can treat it as a
// cancel/drain signal.
func ReadEscape(next func() (byte, error)) (EscapeKind, string, error) {
	first, err := next()
	if err != nil {
		return EscCancel, "", err
	}
	if first == 'b' {
		return EscPageUp, "", nil
	}
	if first == 'f' {
		return EscPageDown, "", nil
	}
	if first != '[' && first != 'O' {
		return EscCancel, "", nil
	}
	seq := []byte{first}
	for len(seq) < 64 {
		b, err := next()
		if err != nil {
			return EscCancel, string(seq), err
		}
		seq = append(seq, b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	s := string(seq)
	if strings.HasPrefix(s, "[<") {
		return parseSGRMouse(s)
	}
	switch s {
	case "[A", "OA":
		return EscUp, "", nil
	case "[B", "OB":
		return EscDown, "", nil
	case "[C", "OC":
		return EscRight, "", nil
	case "[D", "OD":
		return EscLeft, "", nil
	case "[H", "OH", "[1~":
		return EscHome, "", nil
	case "[F", "OF", "[4~":
		return EscEnd, "", nil
	case "[5~":
		return EscPageUp, "", nil
	case "[6~":
		return EscPageDown, "", nil
	default:
		return EscCancel, s, nil
	}
}

// parseSGRMouse decodes an SGR mouse sequence of the form [<button;x;y{M,m}.
// Wheel buttons are 64 (up) and 65 (down); other buttons are reported as a
// generic mouse event carrying the raw sequence text.
func parseSGRMouse(seq string) (EscapeKind, string, error) {
	if !strings.HasSuffix(seq, "M") && !strings.HasSuffix(seq, "m") {
		return EscCancel, seq, nil
	}
	body := strings.TrimPrefix(seq, "[<")
	body = strings.TrimSuffix(strings.TrimSuffix(body, "M"), "m")
	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		return EscCancel, seq, nil
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return EscCancel, seq, nil
	}
	switch button {
	case 64:
		return EscScrollUp, seq, nil
	case 65:
		return EscScrollDn, seq, nil
	default:
		return EscMouse, seq, nil
	}
}
