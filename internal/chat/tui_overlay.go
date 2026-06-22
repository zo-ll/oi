package chat

import (
	"io"
	"strings"

	"github.com/zo-ll/tide"
)

// overlaySurface builds the tide.Overlay the modal widgets draw on: the
// terminal writer, its size, the base repaint callback, and the async byte
// source (which also pumps model events and approval responses).
func (a *tuiApp) overlaySurface() tide.Overlay {
	return tide.Overlay{
		Out:  a.term.Out,
		Size: a.term.Size,
		Base: a.render,
		Next: a.nextByteWithQuit,
	}
}

// nextByteWithQuit feeds bytes to overlay widgets. Ctrl-C (3) and Ctrl-D
// (4) cancel the overlay (return EOF) but do NOT request a full app exit —
// that is handled by the main loop's own case 3,4 when no overlay is open.
func (a *tuiApp) nextByteWithQuit() (byte, error) {
	b, err := a.nextByte()
	if err != nil {
		return b, err
	}
	if b == 3 || b == 4 {
		return 0, io.EOF
	}
	return b, nil
}

func (a *tuiApp) overlayPicker(title string, items []string) (string, bool) {
	a.drainEvents()
	a.overlayCancel = make(chan struct{})
	a.term.DisableMouse()
	defer a.term.EnableMouse()
	return tide.NewPicker(a.overlaySurface()).Open(title, items)
}

// overlayInput opens a single-line prompt modal. Pending app events are
// drained first so that queued renders (e.g. from runLogin writing "Provider:
// X") do not clobber the overlay immediately after it is drawn. Mouse
// tracking is disabled while the modal is open so that click events do not
// generate escape sequences that the widget would interpret as cancel.
func (a *tuiApp) overlayInput(title, prompt, initial string) (string, bool) {
	a.drainEvents()
	a.overlayCancel = make(chan struct{})
	a.term.DisableMouse()
	defer a.term.EnableMouse()
	return tide.NewPrompt(a.overlaySurface()).Open(title, prompt, initial)
}

// drainEvents processes any pending events without blocking.
func (a *tuiApp) drainEvents() {
	for {
		select {
		case fn := <-a.events:
			if fn != nil {
				fn()
			}
		default:
			return
		}
	}
}

// LoginPrompt collects a single line during /login. When required is true it
// re-opens on empty input (e.g. a stray CRLF Enter) instead of returning.
func (a *tuiApp) LoginPrompt(prompt string, required bool) (string, bool) {
	for {
		s, ok := a.overlayInput("oi login", prompt, "")
		if !ok {
			return "", false
		}
		if !required || strings.TrimSpace(s) != "" {
			return s, true
		}
		a.status = "Input required. Press Esc to cancel."
		a.render()
	}
}

// CancelOverlay forces any open overlay to close by signalling the cancel
// channel that readRawByte selects on.
func (a *tuiApp) CancelOverlay() {
	if a.overlayCancel != nil {
		close(a.overlayCancel)
	}
}

// renderApprovalOverlay draws the y/n approval modal. It is oi-specific (the
// action/target domain and channel round-trip belong to the agent layer) but
// reuses tide.DrawBox / tide.WriteClipped for the frame.
func (a *tuiApp) renderApprovalOverlay(action, target string) {
	a.render()
	size := a.term.Size()
	width := size.Cols - 8
	if width > 92 {
		width = 92
	}
	if width < 40 {
		width = 40
	}
	bodyWidth := width - 4
	targetLines := tide.WrapPlain(target, bodyWidth)
	if len(targetLines) > 8 {
		targetLines = append(targetLines[:8], "...")
	}
	height := len(targetLines) + 6
	if height < 7 {
		height = 7
	}
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	tide.DrawBox(a.term.Out, top, left, width, height, tide.Warn("approve "+action))
	row := top + 3
	for _, line := range targetLines {
		tide.WriteClipped(a.term.Out, row, left, width, "| "+line)
		row++
	}
	tide.WriteClipped(a.term.Out, top+height-2, left, width, "| "+tide.Command("y")+" approve   "+tide.Command("n")+" deny   Esc cancel")
	tide.MoveTo(a.term.Out, top+height-2, left+2)
	tide.ShowCursor(a.term.Out)
}
