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

func (ui *terminalUI) readMessage(lastAssistant string) (string, error) {
	var buf []rune
	ui.historyIndex = -1
	ui.historyDraft = ""
	ui.completion = completionState{}
	ui.renderPrompt("")
	defer ui.clearPrompt()
	for {
		select {
		case <-ui.resizeCh:
			ui.renderPrompt(string(buf))
		default:
		}
		b, err := readByte(ui.in)
		if err != nil {
			return "", err
		}
		switch b {
		case '\r', '\n':
			text := strings.TrimRight(string(buf), "\n")
			if strings.TrimSpace(text) == "" {
				buf = buf[:0]
				ui.renderPrompt("")
				continue
			}
			ui.addHistoryEntry(text)
			ui.clearPrompt()
			return text, nil
		case 3:
			ui.completion = completionState{}
			ui.clearPrompt()
			return "", io.EOF
		case 4:
			if len(buf) == 0 {
				ui.completion = completionState{}
				ui.clearPrompt()
				return "", io.EOF
			}
		case 8, 127:
			ui.completion = completionState{}
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				ui.renderPrompt(string(buf))
			} else {
				ui.bell()
			}
		case 9:
			next, status, changed, err := ui.completeAtPath(string(buf))
			if err != nil {
				ui.bell()
				ui.notify("error: " + err.Error())
				ui.renderPrompt(string(buf))
				continue
			}
			if !changed && status == "" {
				buf = append(buf, '\t')
				ui.completion = completionState{}
				ui.renderPrompt(string(buf))
				continue
			}
			if changed {
				buf = []rune(next)
			} else {
				ui.bell()
			}
			ui.renderPrompt(string(buf))
			if status != "" {
				ui.ShowStatus(status)
			}
		case 11:
			ui.completion = completionState{}
			buf = append(buf, '\n')
			ui.renderPrompt(string(buf))
		case 22:
			ui.completion = completionState{}
			text, err := ui.clipboard.Read()
			if err != nil {
				ui.bell()
				ui.clipboardStatus = err.Error()
				continue
			}
			text = normalizePastedText(text)
			if text != "" {
				buf = append(buf, []rune(text)...)
				ui.renderPrompt(string(buf))
			}
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
			ui.renderPrompt(string(buf))
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
				next, ok := ui.historyPrev(string(buf))
				if !ok {
					ui.bell()
					continue
				}
				buf = []rune(next)
				ui.renderPrompt(string(buf))
			case "down":
				next, ok := ui.historyNext()
				if !ok {
					ui.bell()
					continue
				}
				buf = []rune(next)
				ui.renderPrompt(string(buf))
			case "paste":
				ui.completion = completionState{}
				if text != "" {
					buf = append(buf, []rune(normalizePastedText(text))...)
					ui.renderPrompt(string(buf))
				}
			default:
				continue
			}
		default:
			if b >= 32 {
				ui.completion = completionState{}
				buf = append(buf, rune(b))
				ui.renderPrompt(string(buf))
			}
		}
	}
}
