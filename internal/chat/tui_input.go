package chat

import (
	"io"
	"strings"

	"github.com/zo-ll/tide"
)

func (a *tuiApp) nextByte() (byte, error) {
	for {
		select {
		case fn := <-a.events:
			if fn != nil {
				fn()
			}
		case req := <-a.approvals:
			a.approval = &req
			a.renderApprovalOverlay(req.action, req.target)
		case <-a.errCh:
			return 0, io.EOF
		case b := <-a.inputCh:
			if a.handleApprovalInput(b) {
				continue
			}
			return b, nil
		}
	}
}

func (a *tuiApp) handleApprovalInput(b byte) bool {
	if a.approval == nil {
		return false
	}
	switch b {
	case 'y', 'Y':
		a.approval.resp <- true
		a.approval = nil
		a.render()
	case 'n', 'N', 3, 27:
		a.approval.resp <- false
		a.approval = nil
		if b == 27 {
			// ESC cancels the approval; drain the rest of the escape/mouse
			// sequence so its bytes do not leak into the typed input buffer.
			_, _, _ = tide.ReadEscape(a.nextByte)
		}
		a.render()
	default:
		a.renderApprovalOverlay(a.approval.action, a.approval.target)
	}
	return true
}

func (a *tuiApp) addHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(a.history) > 0 && a.history[0] == line {
		a.historyIndex = -1
		a.historyDraft = nil
		return
	}
	a.history = append([]string{line}, a.history...)
	if len(a.history) > 200 {
		a.history = a.history[:200]
	}
	a.historyIndex = -1
	a.historyDraft = nil
}

func (a *tuiApp) historyPrev() {
	if len(a.history) == 0 {
		return
	}
	if a.historyIndex == -1 {
		a.historyDraft = append([]rune(nil), a.input...)
		a.historyIndex = 0
	} else if a.historyIndex+1 < len(a.history) {
		a.historyIndex++
	}
	a.input = []rune(a.history[a.historyIndex])
	a.cursor = len(a.input)
	a.hintIdx = 0
}

func (a *tuiApp) historyNext() {
	if a.historyIndex == -1 {
		return
	}
	if a.historyIndex > 0 {
		a.historyIndex--
		a.input = []rune(a.history[a.historyIndex])
	} else {
		a.historyIndex = -1
		a.input = append([]rune(nil), a.historyDraft...)
		a.historyDraft = nil
	}
	a.cursor = len(a.input)
	a.hintIdx = 0
}

// handleEscape dispatches an escape/cursor-key/mouse sequence using tide's
// normalized ReadEscape. Arrow keys dual-purpose: when slash-command hints are
// visible they navigate hints, otherwise up/down walk prompt history and the
// mouse wheel scrolls the transcript.
func (a *tuiApp) handleEscape() {
	kind, _, err := tide.ReadEscape(a.nextByte)
	if err != nil {
		return
	}
	switch kind {
	case tide.EscPageUp:
		a.scrollPage(1)
	case tide.EscPageDown:
		a.scrollPage(-1)
	case tide.EscUp:
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx > 0 {
				a.hintIdx--
			}
		} else {
			a.historyPrev()
		}
	case tide.EscDown:
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx+1 < len(hints) {
				a.hintIdx++
			}
		} else {
			a.historyNext()
		}
	case tide.EscScrollUp:
		a.scrollLines(3)
	case tide.EscScrollDn:
		a.scrollLines(-3)
	case tide.EscRight:
		if a.cursor < len(a.input) {
			a.cursor++
		}
	case tide.EscLeft:
		if a.cursor > 0 {
			a.cursor--
		}
	case tide.EscHome:
		a.cursor = 0
	case tide.EscEnd:
		a.cursor = len(a.input)
	case tide.EscMouse, tide.EscCancel:
		// no-op: a plain click or bare ESC during input
	}
}

func (a *tuiApp) scrollLines(delta int) {
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
}

func (a *tuiApp) scrollPage(delta int) {
	size := a.term.Size()
	step := size.Rows - 6
	if step < 1 {
		step = 1
	}
	a.scrollLines(delta * step)
}

func (a *tuiApp) commandHints() []string {
	text := string(a.input)
	if len(text) == 0 || text[0] != '/' {
		return nil
	}
	query := strings.TrimPrefix(text, "/")
	return filterByPrefix(chatCommands(), query)
}

func (a *tuiApp) tabComplete() {
	hints := a.commandHints()
	if len(hints) == 0 {
		return
	}
	if a.hintIdx >= len(hints) {
		a.hintIdx = 0
	}
	a.input = []rune(hints[a.hintIdx])
	a.cursor = len(a.input)
}
