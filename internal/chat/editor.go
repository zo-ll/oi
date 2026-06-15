package chat

import (
	"bufio"
	"io"
	"strings"
)

type promptInput struct {
	ui     *terminalUI
	reader *bufio.Reader
	buf    []byte
}

func (p *promptInput) Read(dst []byte) (int, error) {
	if len(p.buf) == 0 {
		if p.ui != nil {
			p.ui.clearPrompt()
			_ = p.ui.suspendRaw()
		}
		line, err := p.reader.ReadString('\n')
		if p.ui != nil {
			_ = p.ui.resumeRaw()
		}
		p.buf = []byte(line)
		if len(p.buf) == 0 && err != nil {
			return 0, err
		}
	}
	n := copy(dst, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

func wrapPromptLines(prompt, text string, width int) []string {
	if width <= len(prompt)+1 {
		width = len(prompt) + 20
	}
	cont := strings.Repeat(" ", len(prompt))
	parts := strings.Split(text, "\n")
	var lines []string
	for i, part := range parts {
		prefix := cont
		if i == 0 || len(lines) == 0 {
			prefix = prompt
		}
		wrapped := wrapLine(part, width-len(prefix))
		if len(wrapped) == 0 {
			lines = append(lines, prefix)
			continue
		}
		for j, piece := range wrapped {
			linePrefix := cont
			if j == 0 {
				linePrefix = prefix
			}
			lines = append(lines, linePrefix+piece)
		}
		if i < len(parts)-1 && part == "" {
			lines = append(lines, cont)
		}
	}
	if len(lines) == 0 {
		return []string{prompt}
	}
	return lines
}

func wrapLine(s string, width int) []string {
	if width <= 1 {
		return []string{s}
	}
	if s == "" {
		return []string{""}
	}
	runes := []rune(s)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		cut := width
		for i := width; i > 0; i-- {
			if runes[i-1] == ' ' || runes[i-1] == '\t' {
				cut = i
				break
			}
		}
		if cut == width {
			for i := width; i < len(runes); i++ {
				if runes[i] == ' ' || runes[i] == '\t' {
					cut = i
					break
				}
			}
		}
		piece := strings.TrimRight(string(runes[:cut]), " \t")
		if piece == "" {
			piece = string(runes[:minInt(width, len(runes))])
			cut = minInt(width, len(runes))
		}
		lines = append(lines, piece)
		runes = trimLeadingSpaceRunes(runes[cut:])
	}
	return lines
}

func trimLeadingSpaceRunes(runes []rune) []rune {
	for len(runes) > 0 && (runes[0] == ' ' || runes[0] == '\t') {
		runes = runes[1:]
	}
	return runes
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func promptCursorPosition(prompt, text string, width, cursor int) (int, int) {
	runes := []rune(text)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	prefix := string(runes[:cursor])
	lines := wrapPromptLines(prompt, prefix, width)
	if len(lines) == 0 {
		return 0, len(prompt)
	}
	return len(lines) - 1, len([]rune(lines[len(lines)-1]))
}

func (ui *terminalUI) readMessage(lastAssistant string) (string, error) {
	var buf []rune
	cursor := 0
	setBuffer := func(text string) {
		buf = []rune(text)
		cursor = len(buf)
	}
	render := func() { ui.renderPromptAt(string(buf), cursor) }
	ui.historyIndex = -1
	ui.historyDraft = ""
	ui.completion = completionState{}
	ui.pickerActive = false
	ui.pickerMatches = nil

	refreshHint := func(current string) {
		ui.pickerActive = false
		ui.pickerMatches = nil
		matches, _ := ui.completionMatchesForText(current)
		if len(matches) == 0 {
			ui.setPromptHint("")
			return
		}
		if len(matches) == 1 {
			ui.setPromptHint(liveHint(matches))
			return
		}
		ui.pickerActive = true
		ui.pickerMatches = matches
		ui.pickerIndex = 0
		ui.setPromptHint(pickerHint(matches, ui.pickerIndex))
	}

	refreshHint("")
	render()
	defer ui.clearPrompt()

	for {
		select {
		case <-ui.resizeCh:
			render()
		default:
		}
		b, err := readByte(ui.in)
		if err != nil {
			return "", err
		}

		switch {
		case ui.pickerActive && b >= '1' && b <= '8':
			idx := int(b - '1')
			if idx >= 0 && idx < len(ui.pickerMatches) {
				next := ui.pickMatch(string(buf), idx)
				setBuffer(next)
				ui.pickerActive = false
				ui.pickerMatches = nil
				refreshHint(string(buf))
				render()
				continue
			}
			ui.bell()
			continue
		}

		switch b {
		case '\r', '\n':
			if ui.pickerActive && len(ui.pickerMatches) > 0 {
				idx := ui.pickerIndex
				if idx < 0 {
					idx = 0
				}
				if idx >= len(ui.pickerMatches) {
					idx = len(ui.pickerMatches) - 1
				}
				next := ui.pickMatch(string(buf), idx)
				setBuffer(next)
				ui.pickerActive = false
				ui.pickerMatches = nil
				if strings.HasPrefix(next, "/") {
					text := strings.TrimRight(next, "\n")
					ui.addHistoryEntry(text)
					ui.setPromptHint("")
					ui.clearPrompt()
					return text, nil
				}
				refreshHint(string(buf))
				render()
				continue
			}
			text := strings.TrimRight(string(buf), "\n")
			if strings.TrimSpace(text) == "" {
				buf = buf[:0]
				cursor = 0
				ui.setPromptHint("")
				ui.pickerActive = false
				ui.pickerMatches = nil
				render()
				continue
			}
			ui.addHistoryEntry(text)
			ui.setPromptHint("")
			ui.clearPrompt()
			return text, nil
		case 3:
			ui.completion = completionState{}
			ui.setPromptHint("")
			ui.pickerActive = false
			ui.pickerMatches = nil
			ui.clearPrompt()
			return "", io.EOF
		case 4:
			if len(buf) == 0 {
				ui.completion = completionState{}
				ui.setPromptHint("")
				ui.pickerActive = false
				ui.pickerMatches = nil
				ui.clearPrompt()
				return "", io.EOF
			}
		case 8, 127:
			ui.completion = completionState{}
			ui.pickerActive = false
			ui.pickerMatches = nil
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
				refreshHint(string(buf))
				render()
			} else {
				ui.setPromptHint("")
				render()
				ui.bell()
			}
			continue
		case 9:
			if cursor != len(buf) {
				ui.bell()
				render()
				continue
			}
			next, _, _, replaced, err := ui.completeAtPath(string(buf))
			if err != nil {
				ui.bell()
				render()
				continue
			}
			if replaced {
				setBuffer(next)
				ui.pickerActive = false
				ui.pickerMatches = nil
				refreshHint(string(buf))
				render()
				continue
			}
			ui.bell()
			render()
			continue
		case 11:
			ui.completion = completionState{}
			ui.pickerActive = false
			ui.pickerMatches = nil
			buf = append(buf[:cursor], append([]rune{'\n'}, buf[cursor:]...)...)
			cursor++
			refreshHint(string(buf))
			render()
			continue
		case 22:
			ui.completion = completionState{}
			ui.pickerActive = false
			ui.pickerMatches = nil
			text, err := ui.clipboard.Read()
			if err != nil {
				ui.bell()
				ui.clipboardStatus = err.Error()
				continue
			}
			text = normalizePastedText(text)
			if text != "" {
				paste := []rune(text)
				buf = append(buf[:cursor], append(paste, buf[cursor:]...)...)
				cursor += len(paste)
				refreshHint(string(buf))
				render()
			}
			continue
		case 25:
			ui.completion = completionState{}
			if strings.TrimSpace(lastAssistant) == "" {
				ui.bell()
				continue
			}
			if err := ui.clipboard.Write(lastAssistant); err != nil {
				ui.bell()
				ui.clipboardStatus = err.Error()
				continue
			}
			ui.notify("copied last reply")
			render()
			continue
		case 27:
			kind, text, handled, err := ui.readEscapeSequence()
			if err != nil {
				return "", err
			}
			if !handled {
				ui.bell()
				continue
			}
			switch kind {
			case "up":
				if ui.pickerActive && len(ui.pickerMatches) > 0 {
					ui.pickerIndex--
					if ui.pickerIndex < 0 {
						ui.pickerIndex = len(ui.pickerMatches) - 1
					}
					ui.setPromptHint(pickerHint(ui.pickerMatches, ui.pickerIndex))
					render()
					continue
				}
				next, ok := ui.historyPrev(string(buf))
				if !ok {
					ui.bell()
					continue
				}
				setBuffer(next)
				ui.pickerActive = false
				ui.pickerMatches = nil
				refreshHint(string(buf))
				render()
				continue
			case "down":
				if ui.pickerActive && len(ui.pickerMatches) > 0 {
					ui.pickerIndex++
					if ui.pickerIndex >= len(ui.pickerMatches) {
						ui.pickerIndex = 0
					}
					ui.setPromptHint(pickerHint(ui.pickerMatches, ui.pickerIndex))
					render()
					continue
				}
				next, ok := ui.historyNext()
				if !ok {
					ui.bell()
					continue
				}
				setBuffer(next)
				ui.pickerActive = false
				ui.pickerMatches = nil
				refreshHint(string(buf))
				render()
				continue
			case "left":
				if cursor > 0 {
					cursor--
					render()
				} else {
					ui.bell()
				}
				continue
			case "right":
				if cursor < len(buf) {
					cursor++
					render()
				} else {
					ui.bell()
				}
				continue
			case "home":
				cursor = 0
				render()
				continue
			case "end":
				cursor = len(buf)
				render()
				continue
			case "delete":
				ui.completion = completionState{}
				ui.pickerActive = false
				ui.pickerMatches = nil
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
					refreshHint(string(buf))
					render()
				} else {
					ui.bell()
				}
				continue
			case "paste":
				ui.completion = completionState{}
				ui.pickerActive = false
				ui.pickerMatches = nil
				if text != "" {
					paste := []rune(normalizePastedText(text))
					buf = append(buf[:cursor], append(paste, buf[cursor:]...)...)
					cursor += len(paste)
					refreshHint(string(buf))
					render()
				}
				continue
			default:
				continue
			}
		default:
			if b >= 32 {
				ui.completion = completionState{}
				ui.pickerActive = false
				ui.pickerMatches = nil
				buf = append(buf[:cursor], append([]rune{rune(b)}, buf[cursor:]...)...)
				cursor++
				refreshHint(string(buf))
				render()
			}
		}
	}
}
