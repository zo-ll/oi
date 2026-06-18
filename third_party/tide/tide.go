package tide

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	hintIndex    int
	hintActive   bool

	mu           sync.Mutex
	renderBuf    []rune
	renderCursor int
	status       string
	statusRows   int
	writeCol     int
	writePending string
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

func (e *Editor) ShowStatus(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = strings.TrimSpace(text)
	if e.renderedRows > 0 {
		e.renderLocked(e.renderBuf, e.renderCursor)
		return
	}
	e.renderStandaloneStatusLocked()

}

func (e *Editor) ClearStandaloneStatus() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearStandaloneStatusLocked()
}

func (e *Editor) renderStandaloneStatusLocked() {
	e.clearStandaloneStatusLocked()
	if e.status == "" {
		return
	}
	lines := e.statusLines()
	for i, line := range lines {
		if i > 0 {
			_, _ = io.WriteString(e.out, "\r\n")
		}
		_, _ = io.WriteString(e.out, "\r\x1b[2K")
		_, _ = io.WriteString(e.out, line)
	}
	e.statusRows = len(lines)
}

func (e *Editor) clearStandaloneStatusLocked() {
	if e.statusRows == 0 {
		return
	}
	if e.statusRows > 1 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", e.statusRows-1))
	}
	_, _ = io.WriteString(e.out, "\r\x1b[J")
	e.statusRows = 0
}

func (e *Editor) ClearStatus() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = ""
	if e.renderedRows > 0 {
		e.renderLocked(e.renderBuf, e.renderCursor)
		return
	}
	e.clearStandaloneStatusLocked()
}

func (e *Editor) Write(p []byte) (int, error) {
	e.mu.Lock()
	e.clearStandaloneStatusLocked()
	err := e.writeWrappedLocked(string(p))
	e.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (e *Editor) writeWrappedLocked(s string) error {
	for len(s) > 0 {
		if strings.HasPrefix(s, "\x1b[") {
			seq, rest := splitANSI(s)
			e.writePending += seq
			s = rest
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 0 {
			break
		}
		s = s[size:]
		switch r {
		case '\r':
			if err := e.flushPendingWordLocked(); err != nil {
				return err
			}
			if _, err := io.WriteString(e.out, "\r"); err != nil {
				return err
			}
			e.writeCol = 0
		case '\n':
			if err := e.flushPendingWordLocked(); err != nil {
				return err
			}
			if _, err := io.WriteString(e.out, "\n"); err != nil {
				return err
			}
			e.writeCol = 0
		case ' ', '\t':
			if err := e.flushPendingWordLocked(); err != nil {
				return err
			}
			w := runeWidth(r)
			if w <= 0 {
				w = 1
			}
			if e.writeCol+w > safeRenderWidth(e.width) {
				if _, err := io.WriteString(e.out, "\n"); err != nil {
					return err
				}
				e.writeCol = 0
				continue
			}
			if _, err := io.WriteString(e.out, string(r)); err != nil {
				return err
			}
			e.writeCol += w
		default:
			e.writePending += string(r)
		}
	}
	return nil
}

func (e *Editor) flushPendingWordLocked() error {
	if e.writePending == "" {
		return nil
	}
	w := displayWidth(stripANSI(e.writePending))
	if e.writeCol > 0 && e.writeCol+w > safeRenderWidth(e.width) {
		if _, err := io.WriteString(e.out, "\n"); err != nil {
			return err
		}
		e.writeCol = 0
	}
	if _, err := io.WriteString(e.out, e.writePending); err != nil {
		return err
	}
	e.writeCol += w
	e.writePending = ""
	return nil
}

func (e *Editor) Styled(kind, text string) string {
	switch kind {
	case "dim":
		return dim(text)
	case "warn":
		return "\x1b[33m" + text + "\x1b[0m"
	case "command":
		return "\x1b[36m" + text + "\x1b[0m"
	default:
		return text
	}
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
	e.hintActive = false
	e.hintIndex = 0
	refreshHint := func(current string) {
		e.hint = nil
		e.hintActive = false
		if e.complete == nil || len(current) == 0 || current[0] != '/' {
			return
		}
		completion, err := e.complete(current)
		if err != nil || len(completion.Matches) == 0 {
			return
		}
		e.hint = completion.Matches
		e.hintActive = true
		e.hintIndex = 0
	}
	refreshHint("")
	e.render(buf, cursor)
	stopResize := e.watchResize(e.rerenderSaved)
	defer stopResize()

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
			e.hintActive = false
			e.render(buf, cursor)
			_, _ = io.WriteString(e.out, "\r\n")
			e.markClean()
			if strings.TrimSpace(text) != "" {
				e.addHistory(text)
			}
			return text, nil
		case 3:
			e.hint = nil
			e.hintActive = false
			e.clear()
			return "", io.EOF
		case 4:
			if len(buf) == 0 {
				e.hint = nil
				e.hintActive = false
				e.clear()
				return "", io.EOF
			}
		case 1:
			cursor = 0
			e.render(buf, cursor)
		case 5:
			cursor = len(buf)
			e.render(buf, cursor)
		case 2:
			cursor = wordLeft(buf, cursor)
			e.render(buf, cursor)
		case 6:
			cursor = wordRight(buf, cursor)
			e.render(buf, cursor)
		case 8, 127:
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
				refreshHint(string(buf))
				e.render(buf, cursor)
			} else {
				e.bell()
			}
		case 9:
			if e.hintActive && len(e.hint) > 0 {
				buf = []rune(e.hint[e.hintIndex])
				cursor = len(buf)
				refreshHint(string(buf))
				e.render(buf, cursor)
				continue
			}
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
			e.hintActive = len(completion.Matches) > 0
			e.hintIndex = 0
			if completion.Text == "" && len(completion.Matches) == 0 {
				e.bell()
			}
			e.render(buf, cursor)
		case 11:
			buf = buf[:cursor]
			refreshHint(string(buf))
			e.render(buf, cursor)
		case 21:
			buf = buf[cursor:]
			cursor = 0
			refreshHint(string(buf))
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
				if e.hintActive && len(e.hint) > 0 {
					e.hintIndex--
					if e.hintIndex < 0 {
						e.hintIndex = len(e.hint) - 1
					}
					e.render(buf, cursor)
					continue
				}
				if next, ok := e.historyPrev(string(buf)); ok {
					buf = []rune(next)
					cursor = len(buf)
					refreshHint(string(buf))
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "down":
				if e.hintActive && len(e.hint) > 0 {
					e.hintIndex++
					if e.hintIndex >= len(e.hint) {
						e.hintIndex = 0
					}
					e.render(buf, cursor)
					continue
				}
				if next, ok := e.historyNext(); ok {
					buf = []rune(next)
					cursor = len(buf)
					refreshHint(string(buf))
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
			case "word-left":
				cursor = wordLeft(buf, cursor)
				e.render(buf, cursor)
			case "word-right":
				cursor = wordRight(buf, cursor)
				e.render(buf, cursor)
			case "home":
				cursor = 0
				e.render(buf, cursor)
			case "end":
				cursor = len(buf)
				e.render(buf, cursor)
			case "delete":
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
					refreshHint(string(buf))
					e.render(buf, cursor)
				} else {
					e.bell()
				}
			case "paste":
				paste := []rune(normalizePaste(text))
				buf = append(buf[:cursor], append(paste, buf[cursor:]...)...)
				cursor += len(paste)
				refreshHint(string(buf))
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
				refreshHint(string(buf))
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
	var draw func()
	refilter := func() {
		filtered = filterItems(items, query)
		if idx >= len(filtered) {
			idx = len(filtered) - 1
		}
		if idx < 0 {
			idx = 0
		}
	}
	draw = func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		clearRows(e.out, rows)
		rows = 0
		writeLine := func(text string) {
			_, _ = io.WriteString(e.out, "\r\x1b[2K")
			_, _ = io.WriteString(e.out, dim(text))
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
		start := idx - idx%5
		if start+5 > len(filtered) {
			start = len(filtered) - 5
		}
		if start < 0 {
			start = 0
		}
		end := start + 5
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
	stopResize := e.watchResize(func() {
		if draw != nil {
			draw()
		}
	})
	defer clearRows(e.out, rows)
	defer stopResize()

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
	e.mu.Lock()
	defer e.mu.Unlock()
	e.renderLocked(buf, cursor)
}

func (e *Editor) rerenderSaved() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.renderedRows > 0 {
		e.renderLocked(e.renderBuf, e.renderCursor)
	}
}

func (e *Editor) markClean() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.renderedRows = 0
}

func (e *Editor) renderLocked(buf []rune, cursor int) {
	if e.width <= 0 {
		e.width = terminalWidth(e.in)
	}
	e.clearStandaloneStatusLocked()
	e.renderBuf = append(e.renderBuf[:0], buf...)
	e.renderCursor = cursor
	if e.renderedRows > 0 {
		if e.renderedRows > 1 {
			_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", e.renderedRows-1))
		}
		_, _ = io.WriteString(e.out, "\r\x1b[J")
	}

	lines := wrapPromptLines(e.prompt, string(buf), e.width)
	all := e.statusLines()
	all = append(all, e.hintLines()...)
	all = append(all, lines...)
	for i, line := range all {
		if i > 0 {
			_, _ = io.WriteString(e.out, "\r\n")
		}
		_, _ = io.WriteString(e.out, line)
	}
	e.renderedRows = len(all)

	promptRow, col := promptCursorPosition(e.prompt, string(buf), e.width, cursor)
	row := len(e.statusLines()) + len(e.hintLines()) + promptRow
	if up := len(all) - 1 - row; up > 0 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dA", up))
	}
	_, _ = io.WriteString(e.out, "\r")
	if col > 0 {
		_, _ = io.WriteString(e.out, fmt.Sprintf("\x1b[%dC", col))
	}
}

func (e *Editor) clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearLocked()
}

func (e *Editor) clearLocked() {
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
	if !e.hintActive || len(e.hint) == 0 {
		return nil
	}
	pageSize := 5
	start := e.hintIndex - pageSize/2
	if start < 0 {
		start = 0
	}
	end := start + pageSize
	if end > len(e.hint) {
		end = len(e.hint)
		start = end - pageSize
		if start < 0 {
			start = 0
		}
	}
	var lines []string
	for i := start; i < end; i++ {
		marker := "  "
		if i == e.hintIndex {
			marker = "> "
		}
		lines = append(lines, dim(marker+e.hint[i]))
	}
	return lines
}

func (e *Editor) statusLines() []string {
	if e.status == "" {
		return nil
	}
	lines := wrapLine(e.status, e.width)
	for i := range lines {
		lines[i] = dim(lines[i])
	}
	return lines
}

func dim(text string) string {
	return "\x1b[2m" + text + "\x1b[0m"
}

func splitANSI(s string) (seq, rest string) {
	if !strings.HasPrefix(s, "\x1b[") {
		return "", s
	}
	for i := 2; i < len(s); i++ {
		b := s[i]
		if b >= 0x40 && b <= 0x7e {
			return s[:i+1], s[i+1:]
		}
	}
	return s, ""
}

func stripANSI(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		if strings.HasPrefix(s, "\x1b[") {
			_, rest := splitANSI(s)
			s = rest
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 0 {
			break
		}
		b.WriteRune(r)
		s = s[size:]
	}
	return b.String()
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
	if first == 'b' {
		return "word-left", "", true, nil
	}
	if first == 'f' {
		return "word-right", "", true, nil
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
	case "[1;3D", "[1;5D", "[5D":
		return "word-left", "", true, nil
	case "[1;3C", "[1;5C", "[5C":
		return "word-right", "", true, nil
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
	width = safeRenderWidth(width)
	promptW := displayWidth(prompt)
	if width <= promptW+1 {
		width = promptW + 20
	}
	cont := strings.Repeat(" ", promptW)
	parts := strings.Split(text, "\n")
	var lines []string
	for i, part := range parts {
		prefix := cont
		if i == 0 {
			prefix = prompt
		}
		pieces := wrapLineFromRunes([]rune(part), width-displayWidth(prefix))
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
	width = safeRenderWidth(width)
	if width <= 1 || s == "" {
		return []string{s}
	}
	return wrapLineFromRunes([]rune(s), width)
}

func safeRenderWidth(width int) int {
	if width > 1 {
		return width - 1
	}
	return width
}

func wrapLineFromRunes(runes []rune, width int) []string {
	if width <= 1 || len(runes) == 0 {
		return []string{string(runes)}
	}
	var lines []string
	for len(runes) > 0 {
		used := 0
		cut := 0
		lastSpace := -1
		for cut < len(runes) {
			rw := runeWidth(runes[cut])
			if rw < 0 {
				rw = 0
			}
			if cut > 0 && used+rw > width {
				break
			}
			used += rw
			if isWordSpace(runes[cut]) {
				lastSpace = cut
			}
			cut++
			if used >= width {
				break
			}
		}
		if cut >= len(runes) {
			lines = append(lines, string(runes))
			break
		}
		if lastSpace > 0 {
			lines = append(lines, string(trimRightSpaces(runes[:lastSpace])))
			runes = trimLeftSpaces(runes[lastSpace+1:])
			continue
		}
		if cut == 0 {
			cut = 1
		}
		lines = append(lines, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func trimLeftSpaces(runes []rune) []rune {
	for len(runes) > 0 && isWordSpace(runes[0]) {
		runes = runes[1:]
	}
	return runes
}

func trimRightSpaces(runes []rune) []rune {
	for len(runes) > 0 && isWordSpace(runes[len(runes)-1]) {
		runes = runes[:len(runes)-1]
	}
	return runes
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case isCombining(r):
		return 0
	case isWideRune(r):
		return 2
	default:
		return 1
	}
}

func isCombining(r rune) bool {
	switch {
	case r >= 0x0300 && r <= 0x036f:
	case r >= 0x1ab0 && r <= 0x1aff:
	case r >= 0x1dc0 && r <= 0x1dff:
	case r >= 0x20d0 && r <= 0x20ff:
	case r >= 0xfe20 && r <= 0xfe2f:
	default:
		return false
	}
	return true
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115f:
	case r >= 0x2329 && r <= 0x232a:
	case r >= 0x2e80 && r <= 0xa4cf:
	case r >= 0xac00 && r <= 0xd7a3:
	case r >= 0xf900 && r <= 0xfaff:
	case r >= 0xfe10 && r <= 0xfe19:
	case r >= 0xfe30 && r <= 0xfe6f:
	case r >= 0xff00 && r <= 0xff60:
	case r >= 0xffe0 && r <= 0xffe6:
	case r >= 0x1f300 && r <= 0x1faff:
	case r >= 0x20000 && r <= 0x3fffd:
	default:
		return false
	}
	return true
}

func wordLeft(buf []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	i := cursor
	for i > 0 && isWordSpace(buf[i-1]) {
		i--
	}
	for i > 0 && !isWordSpace(buf[i-1]) {
		i--
	}
	return i
}

func wordRight(buf []rune, cursor int) int {
	if cursor >= len(buf) {
		return len(buf)
	}
	i := cursor
	for i < len(buf) && !isWordSpace(buf[i]) {
		i++
	}
	for i < len(buf) && isWordSpace(buf[i]) {
		i++
	}
	return i
}

func isWordSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func (e *Editor) watchResize(redraw func()) func() {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-ch:
				e.mu.Lock()
				e.width = terminalWidth(e.in)
				e.mu.Unlock()
				if redraw != nil {
					redraw()
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
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
	return len(lines) - 1, displayWidth(lines[len(lines)-1])
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
	if f == nil {
		return "", fmt.Errorf("nil terminal")
	}
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
	if f == nil {
		return fmt.Errorf("nil terminal")
	}
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
