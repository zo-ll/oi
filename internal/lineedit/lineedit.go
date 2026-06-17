package lineedit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Completion struct {
	Text    string
	Matches []string
}

type Completer func(text string) (Completion, error)

type Editor struct {
	in       *os.File
	out      io.Writer
	prompt   string
	width    int
	complete Completer

	history      []string
	historyIndex int
	historyDraft string
	rawState     string
	raw          bool
	renderedRows int
	hint         []string
}

func New(in *os.File, out io.Writer, prompt string, complete Completer) *Editor {
	if prompt == "" {
		prompt = "> "
	}
	return &Editor{in: in, out: out, prompt: prompt, width: terminalWidth(in), complete: complete, historyIndex: -1}
}

func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (e *Editor) Close() error {
	return e.disableRaw()
}

func (e *Editor) ReadLine() (string, error) {
	if err := e.enableRaw(); err != nil {
		return "", err
	}
	defer e.disableRaw()

	var buf []rune
	cursor := 0
	e.historyIndex = -1
	e.historyDraft = ""
	e.hint = nil
	e.render(buf, cursor)

	for {
		b, err := readByte(e.in)
		if err != nil {
			e.clear()
			return "", err
		}
		switch b {
		case '\r', '\n':
			text := strings.TrimRight(string(buf), "\n")
			e.hint = nil
			e.render(buf, cursor)
			_, _ = io.WriteString(e.out, "\r\n")
			e.renderedRows = 0
			if strings.TrimSpace(text) != "" {
				e.addHistory(text)
			}
			return text, nil
		case 3:
			e.clear()
			return "", io.EOF
		case 4:
			if len(buf) == 0 {
				e.clear()
				return "", io.EOF
			}
		case 1:
			cursor = 0
			e.render(buf, cursor)
		case 5:
			cursor = len(buf)
			e.render(buf, cursor)
		case 8, 127:
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
				e.hint = nil
				e.render(buf, cursor)
			} else {
				e.bell()
			}
		case 9:
			if e.complete == nil || cursor != len(buf) {
				e.bell()
				continue
			}
			completion, err := e.complete(string(buf))
			if err != nil {
				e.bell()
				continue
			}
			if completion.Text != "" && completion.Text != string(buf) {
				buf = []rune(completion.Text)
				cursor = len(buf)
			}
			e.hint = completion.Matches
			if completion.Text == "" && len(completion.Matches) == 0 {
				e.bell()
			}
			e.render(buf, cursor)
		case 11:
			buf = buf[:cursor]
			e.hint = nil
			e.render(buf, cursor)
		case 21:
			buf = buf[cursor:]
			cursor = 0
			e.hint = nil
			e.render(buf, cursor)
		case 27:
			kind, text, handled, err := e.readEscape()
			if err != nil {
				e.clear()
				return "", err
			}
			if !handled {
				e.bell()
				continue
			}
			switch kind {
			case "up":
				if next, ok := e.historyPrev(string(buf)); ok {
					buf = []rune(next)
					cursor = len(buf)
					e.hint = nil
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "down":
				if next, ok := e.historyNext(); ok {
					buf = []rune(next)
					cursor = len(buf)
					e.hint = nil
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "left":
				if cursor > 0 {
					cursor--
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "right":
				if cursor < len(buf) {
					cursor++
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "home":
				cursor = 0
				e.render(buf, cursor)
			case "end":
				cursor = len(buf)
				e.render(buf, cursor)
			case "delete":
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
					e.hint = nil
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "paste":
				paste := []rune(normalizePaste(text))
				buf = append(buf[:cursor], append(paste, buf[cursor:]...)...)
				cursor += len(paste)
				e.hint = nil
				e.render(buf, cursor)
			}
		default:
			if b >= 32 {
				r, size := rune(b), 1
				if b >= utf8.RuneSelf {
					var more [utf8.UTFMax]byte
					more[0] = b
					for size < utf8.UTFMax && !utf8.FullRune(more[:size]) {
						next, err := readByte(e.in)
						if err != nil {
							e.clear()
							return "", err
						}
						more[size] = next
						size++
					}
					r, _ = utf8.DecodeRune(more[:size])
				}
				buf = append(buf[:cursor], append([]rune{r}, buf[cursor:]...)...)
				cursor++
				e.hint = nil
				e.render(buf, cursor)
			}
		}
	}
}

func (e *Editor) Select(title string, items []string) (string, bool, error) {
	if len(items) == 0 {
		return "", false, nil
	}
	if err := e.enableRaw(); err != nil {
		return "", false, err
	}
	defer e.disableRaw()

	idx := 0
	query := ""
	filtered := append([]string(nil), items...)
	rows := 0
	refilter := func() {
		filtered = filterItems(items, query)
		if idx >= len(filtered) {
			idx = len(filtered) - 1
		}
		if idx < 0 {
			idx = 0
		}
	}
	draw := func() {
		clearRows(e.out, rows)
		rows = 0
		writeLine := func(text string) {
			_, _ = io.WriteString(e.out, "\r\x1b[2K")
			_, _ = io.WriteString(e.out, text)
			_, _ = io.WriteString(e.out, "\r\n")
			rows++
		}
		header := title + "  type filter  up/down nav  enter pick  ctrl-c cancel"
		if query != "" {
			header += "  filter: " + query
		}
		for _, line := range wrapLine(header, e.width) {
			writeLine(line)
		}
		if len(filtered) == 0 {
			writeLine("  no matches")
			return
		}
		start := idx - idx%8
		if start+8 > len(filtered) {
			start = len(filtered) - 8
		}
		if start < 0 {
			start = 0
		}
		end := start + 8
		if end > len(filtered) {
			end = len(filtered)
		}
		for i := start; i < end; i++ {
			marker := "  "
			if i == idx {
				marker = "> "
			}
			for _, line := range wrapLine(marker+filtered[i], e.width) {
				writeLine(line)
			}
		}
	}
	draw()
	defer clearRows(e.out, rows)

	for {
		b, err := readByte(e.in)
		if err != nil {
			return "", false, err
		}
		switch b {
		case 3:
			return "", false, nil
		case '\r', '\n':
			if len(filtered) == 0 {
				e.bell()
				continue
			}
			return filtered[idx], true, nil
		case 8, 127:
			if query == "" {
				e.bell()
				continue
			}
			runes := []rune(query)
			query = string(runes[:len(runes)-1])
			refilter()
			draw()
		case 27:
			kind, _, handled, err := e.readEscape()
			if err != nil {
				return "", false, err
			}
			if !handled {
				return "", false, nil
			}
			switch kind {
			case "up":
				if len(filtered) == 0 {
					e.bell()
					continue
				}
				idx--
				if idx < 0 {
					idx = len(filtered) - 1
				}
				draw()
			case "down":
				if len(filtered) == 0 {
					e.bell()
					continue
				}
				idx++
				if idx >= len(filtered) {
					idx = 0
				}
				draw()
			default:
				return "", false, nil
			}
		default:
			if b >= 32 {
				query += string(rune(b))
				refilter()
				draw()
			} else {
				e.bell()
			}
		}
	}
}

func (e *Editor) addHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == text {
		return
	}
	e.history = append(e.history, text)
	if len(e.history) > 200 {
		e.history = e.history[len(e.history)-200:]
	}
}

func (e *Editor) historyPrev(current string) (string, bool) {
	if len(e.history) == 0 {
		return "", false
	}
	if e.historyIndex == -1 {
		e.historyDraft = current
		e.historyIndex = len(e.history) - 1
	} else if e.historyIndex > 0 {
		e.historyIndex--
	} else {
		return "", false
	}
	return e.history[e.historyIndex], true
}

func (e *Editor) historyNext() (string, bool) {
	if e.historyIndex == -1 {
		return "", false
	}
	if e.historyIndex < len(e.history)-1 {
		e.historyIndex++
		return e.history[e.historyIndex], true
	}
	e.historyIndex = -1
	return e.historyDraft, true
}

func (e *Editor) render(buf []rune, cursor int) {
	if e.width <= 0 {
		e.width = terminalWidth(e.in)
	}
	if e.renderedRows > 0 {
		if e.renderedRows > 1 {
			_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", e.renderedRows-1))
		}
		_, _ = io.WriteString(e.out, "\r\x1b[J")
	}

	lines := wrapPromptLines(e.prompt, string(buf), e.width)
	all := append([]string(nil), e.hintLines()...)
	all = append(all, lines...)
	for i, line := range all {
		if i > 0 {
			_, _ = io.WriteString(e.out, "\r\n")
		}
		_, _ = io.WriteString(e.out, line)
	}
	e.renderedRows = len(all)

	promptRow, col := promptCursorPosition(e.prompt, string(buf), e.width, cursor)
	row := len(e.hintLines()) + promptRow
	if up := len(all) - 1 - row; up > 0 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", up))
	}
	_, _ = io.WriteString(e.out, "\r")
	if col > 0 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dC", col))
	}
}

func (e *Editor) clear() {
	if e.renderedRows == 0 {
		return
	}
	if e.renderedRows > 1 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", e.renderedRows-1))
	}
	_, _ = io.WriteString(e.out, "\r\x1b[J")
	e.renderedRows = 0
}

func clearRows(out io.Writer, rows int) {
	if rows <= 0 {
		return
	}
	_, _ = io.WriteString(out, "\r")
	if rows > 0 {
		_, _ = io.WriteString(out, fmt.Sprintf("\x1b[%dA", rows))
	}
	for i := 0; i < rows; i++ {
		_, _ = io.WriteString(out, "\r\x1b[2K")
		if i < rows-1 {
			_, _ = io.WriteString(out, "\x1b[1B")
		}
	}
	if rows > 1 {
		_, _ = io.WriteString(out, fmt.Sprintf("\x1b[%dA", rows-1))
	}
	_, _ = io.WriteString(out, "\r")
}

func filterItems(items []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]string(nil), items...)
	}
	var out []string
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), query) {
			out = append(out, item)
		}
	}
	return out
}

func (e *Editor) hintLines() []string {
	if len(e.hint) == 0 {
		return nil
	}
	limit := len(e.hint)
	if limit > 8 {
		limit = 8
	}
	return []string{"tab: " + strings.Join(e.hint[:limit], "  ")}
}

func (e *Editor) bell() {
	_, _ = io.WriteString(e.out, "\a")
}

func (e *Editor) enableRaw() error {
	if e.raw {
		return nil
	}
	state, err := sttyCapture(e.in, "-g")
	if err != nil {
		return err
	}
	if err := sttyRun(e.in, "raw", "-echo"); err != nil {
		return err
	}
	e.rawState = strings.TrimSpace(state)
	e.raw = true
	_, _ = io.WriteString(e.out, "\x1b[?2004h")
	return nil
}

func (e *Editor) disableRaw() error {
	if !e.raw {
		return nil
	}
	_, _ = io.WriteString(e.out, "\x1b[?2004l")
	e.raw = false
	if e.rawState == "" {
		return nil
	}
	return sttyRun(e.in, e.rawState)
}

func readByte(f *os.File) (byte, error) {
	var buf [1]byte
	_, err := f.Read(buf[:])
	return buf[0], err
}

func (e *Editor) readEscape() (kind, text string, handled bool, err error) {
	first, err := readByte(e.in)
	if err != nil {
		return "", "", false, err
	}
	if first != '[' {
		return "", "", false, nil
	}
	var seq bytes.Buffer
	seq.WriteByte(first)
	for seq.Len() < 16 {
		b, err := readByte(e.in)
		if err != nil {
			return "", "", false, err
		}
		seq.WriteByte(b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	switch seq.String() {
	case "[A":
		return "up", "", true, nil
	case "[B":
		return "down", "", true, nil
	case "[C":
		return "right", "", true, nil
	case "[D":
		return "left", "", true, nil
	case "[H", "[1~":
		return "home", "", true, nil
	case "[F", "[4~":
		return "end", "", true, nil
	case "[3~":
		return "delete", "", true, nil
	case "[200~":
		text, err := readBracketedPaste(e.in)
		return "paste", text, true, err
	default:
		return "", "", false, nil
	}
}

func readBracketedPaste(f *os.File) (string, error) {
	var data []byte
	needle := []byte{27, '[', '2', '0', '1', '~'}
	match := 0
	for {
		b, err := readByte(f)
		if err != nil {
			return "", err
		}
		if b == needle[match] {
			match++
			if match == len(needle) {
				return string(data), nil
			}
			continue
		}
		if match > 0 {
			data = append(data, needle[:match]...)
			match = 0
		}
		data = append(data, b)
	}
}

func normalizePaste(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
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
		if i == 0 {
			prefix = prompt
		}
		pieces := wrapLine(part, width-len(prefix))
		if len(pieces) == 0 {
			lines = append(lines, prefix)
			continue
		}
		for j, piece := range pieces {
			linePrefix := cont
			if j == 0 {
				linePrefix = prefix
			}
			lines = append(lines, linePrefix+piece)
		}
	}
	if len(lines) == 0 {
		return []string{prompt}
	}
	return lines
}

func wrapLine(s string, width int) []string {
	if width <= 1 || s == "" {
		return []string{s}
	}
	runes := []rune(s)
	var lines []string
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return lines
}

func promptCursorPosition(prompt, text string, width, cursor int) (int, int) {
	runes := []rune(text)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	lines := wrapPromptLines(prompt, string(runes[:cursor]), width)
	return len(lines) - 1, len([]rune(lines[len(lines)-1]))
}

func terminalWidth(f *os.File) int {
	out, err := sttyCapture(f, "size")
	if err == nil {
		parts := strings.Fields(strings.TrimSpace(out))
		if len(parts) == 2 {
			if width, convErr := strconv.Atoi(parts[1]); convErr == nil && width > 0 {
				return width
			}
		}
	}
	return 80
}

func sttyCapture(f *os.File, args ...string) (string, error) {
	name := f.Name()
	for _, flag := range []string{"-F", "-f"} {
		cmdArgs := append([]string{flag, name}, args...)
		cmd := exec.Command("stty", cmdArgs...)
		cmd.Stdin = f
		data, err := cmd.Output()
		if err == nil {
			return string(data), nil
		}
	}
	cmd := exec.Command("stty", args...)
	cmd.Stdin = f
	data, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sttyRun(f *os.File, args ...string) error {
	name := f.Name()
	for _, flag := range []string{"-F", "-f"} {
		cmdArgs := append([]string{flag, name}, args...)
		cmd := exec.Command("stty", cmdArgs...)
		cmd.Stdin = f
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	cmd := exec.Command("stty", args...)
	cmd.Stdin = f
	return cmd.Run()
}
