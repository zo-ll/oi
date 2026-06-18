package chat

import (
	"io"
	"os"
	"strconv"
	"strings"
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

func (a *tuiApp) handleEscape() {
	first, err := a.nextByte()
	if err != nil {
		return
	}
	if first == 'b' {
		a.scrollPage(1)
		return
	}
	if first == 'f' {
		a.scrollPage(-1)
		return
	}
	if first != '[' && first != 'O' {
		return
	}
	seq := []byte{first}
	for len(seq) < 64 {
		b, err := a.nextByte()
		if err != nil {
			return
		}
		seq = append(seq, b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	if strings.HasPrefix(string(seq), "[<") {
		a.handleSGRMouse(string(seq))
		return
	}
	switch string(seq) {
	case "[A", "OA":
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx > 0 {
				a.hintIdx--
			}
		} else {
			a.historyPrev()
		}
	case "[B", "OB":
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx+1 < len(hints) {
				a.hintIdx++
			}
		} else {
			a.historyNext()
		}
	case "[5~":
		a.scrollPage(1)
	case "[6~":
		a.scrollPage(-1)
	case "[C", "OC":
		if a.cursor < len(a.input) {
			a.cursor++
		}
	case "[D", "OD":
		if a.cursor > 0 {
			a.cursor--
		}
	case "[H", "OH", "[1~":
		a.cursor = 0
	case "[F", "OF", "[4~":
		a.cursor = len(a.input)
	}
}

func (a *tuiApp) handleSGRMouse(seq string) {
	if !strings.HasSuffix(seq, "M") && !strings.HasSuffix(seq, "m") {
		return
	}
	body := strings.TrimPrefix(seq, "[<")
	body = strings.TrimSuffix(strings.TrimSuffix(body, "M"), "m")
	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		return
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	switch button {
	case 64:
		a.scrollLines(3)
	case 65:
		a.scrollLines(-3)
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

func (a *tuiApp) readOverlayEscape() string {
	first, err := a.nextByte()
	if err != nil {
		return "cancel"
	}
	if first != '[' && first != 'O' {
		return "cancel"
	}
	seq := []byte{first}
	for len(seq) < 16 {
		b, err := a.nextByte()
		if err != nil {
			return "cancel"
		}
		seq = append(seq, b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	switch string(seq) {
	case "[A", "OA":
		return "up"
	case "[B", "OB":
		return "down"
	case "[C", "OC":
		return "right"
	case "[D", "OD":
		return "left"
	case "[H", "OH", "[1~":
		return "home"
	case "[F", "OF", "[4~":
		return "end"
	case "[5~":
		return "page-up"
	case "[6~":
		return "page-down"
	default:
		return "cancel"
	}
}

func readTUIByte(f *os.File) (byte, error) {
	var buf [1]byte
	_, err := f.Read(buf[:])
	return buf[0], err
}
