package chat

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

type terminalUI struct {
	in              *os.File
	out             *os.File
	width           int
	header          string
	headerLines     int
	prompt          string
	promptLines     int
	promptCursorRow int
	outputColumn    int
	editing         bool
	raw             bool
	sttyState       string
	resizeCh        chan os.Signal
	clipboard       clipboard
	clipboardStatus string
	statusVisible   bool
	workspaceRoot   string
	fileList        []string
	history         []string
	historyIndex    int
	historyDraft    string
	completion      completionState
	promptHint      string
	promptHintLines int
	pickerMatches   []string
	pickerActive    bool
	pickerIndex     int
	mu              sync.Mutex
}

func newTerminalUI(in io.Reader, out io.Writer) (*terminalUI, bool) {
	inFile, ok := in.(*os.File)
	if !ok || !isCharDevice(inFile) {
		return nil, false
	}
	outFile, ok := out.(*os.File)
	if !ok || !isCharDevice(outFile) {
		return nil, false
	}
	ui := &terminalUI{
		in:           inFile,
		out:          outFile,
		width:        terminalWidth(inFile),
		prompt:       "> ",
		resizeCh:     make(chan os.Signal, 1),
		clipboard:    clipboard{out: outFile},
		historyIndex: -1,
	}
	signal.Notify(ui.resizeCh, syscall.SIGWINCH)
	return ui, true
}

func (ui *terminalUI) Close() {
	if ui == nil {
		return
	}
	signal.Stop(ui.resizeCh)
	close(ui.resizeCh)
	_ = ui.disableRawMode()
}

func (ui *terminalUI) enableRawMode() error {
	if ui == nil || ui.raw {
		return nil
	}
	state, err := sttyCapture(ui.in, "-g")
	if err != nil {
		return err
	}
	if err := sttyRun(ui.in, "raw", "-echo"); err != nil {
		return err
	}
	ui.sttyState = strings.TrimSpace(state)
	ui.raw = true
	_, _ = io.WriteString(ui.out, "\x1b[?2004h")
	return nil
}

func (ui *terminalUI) disableRawMode() error {
	if ui == nil || !ui.raw {
		return nil
	}
	_, _ = io.WriteString(ui.out, "\x1b[?2004l")
	ui.raw = false
	if ui.sttyState == "" {
		return nil
	}
	return sttyRun(ui.in, ui.sttyState)
}

func (ui *terminalUI) suspendRaw() error {
	return ui.disableRawMode()
}

func (ui *terminalUI) resumeRaw() error {
	return ui.enableRawMode()
}

func (ui *terminalUI) refreshSize() {
	if ui == nil || ui.in == nil || !isCharDevice(ui.in) {
		return
	}
	width := terminalWidth(ui.in)
	if width > 0 {
		ui.width = width
	}
}

func (ui *terminalUI) renderPrompt(text string) {
	ui.renderPromptAt(text, len([]rune(text)))
}

func (ui *terminalUI) renderPromptAt(text string, cursor int) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.refreshSize()
	ui.clearStatusLocked()
	ui.clearPromptLocked()
	headerLines := ui.wrapHeaderLinesLocked()
	for _, line := range headerLines {
		_, _ = io.WriteString(ui.out, ui.Styled("dim", line))
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.headerLines++
	}
	hintLines := ui.wrapHintLinesLocked()
	for _, line := range hintLines {
		_, _ = io.WriteString(ui.out, ui.Styled("dim", line))
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.promptHintLines++
	}
	lines := wrapPromptLines(ui.prompt, text, ui.width)
	for i, line := range lines {
		if i > 0 {
			_, _ = io.WriteString(ui.out, "\r\n")
		}
		_, _ = io.WriteString(ui.out, line)
	}
	ui.promptLines = len(lines)
	if ui.promptLines == 0 {
		ui.promptLines = 1
	}
	row, col := promptCursorPosition(ui.prompt, text, ui.width, cursor)
	if row < 0 {
		row = 0
	}
	if row >= ui.promptLines {
		row = ui.promptLines - 1
	}
	ui.promptCursorRow = row
	bottom := ui.promptLines - 1
	if up := bottom - row; up > 0 {
		_, _ = io.WriteString(ui.out, fmt.Sprintf("\x1b[%dA", up))
	}
	_, _ = io.WriteString(ui.out, "\r")
	if col > 0 {
		_, _ = io.WriteString(ui.out, fmt.Sprintf("\x1b[%dC", col))
	}
	ui.editing = true
}

func (ui *terminalUI) clearPrompt() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearPromptLocked()
}

func (ui *terminalUI) setPromptHint(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.promptHint = strings.TrimRight(text, "\n")
}

func (ui *terminalUI) setHeader(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.header = strings.TrimRight(text, "\n")
}

func (ui *terminalUI) wrapHeaderLinesLocked() []string {
	text := strings.TrimSpace(ui.header)
	if text == "" {
		return nil
	}
	return wrapLine(text, ui.width)
}

func (ui *terminalUI) wrapHintLinesLocked() []string {
	text := strings.TrimSpace(ui.promptHint)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	var out []string
	for _, part := range parts {
		if part == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapLine(part, ui.width)...)
	}
	return out
}

func (ui *terminalUI) clearPromptLocked() {
	total := ui.headerLines + ui.promptLines + ui.promptHintLines
	if total <= 0 {
		return
	}
	_, _ = io.WriteString(ui.out, "\r")
	if ui.promptLines > 0 && ui.promptCursorRow < ui.promptLines-1 {
		_, _ = io.WriteString(ui.out, fmt.Sprintf("\x1b[%dB", ui.promptLines-1-ui.promptCursorRow))
	}
	for i := 0; i < total-1; i++ {
		_, _ = io.WriteString(ui.out, "\x1b[1A")
	}
	for i := 0; i < total; i++ {
		_, _ = io.WriteString(ui.out, "\r\x1b[2K")
		if i < total-1 {
			_, _ = io.WriteString(ui.out, "\x1b[1B")
		}
	}
	for i := 0; i < total-1; i++ {
		_, _ = io.WriteString(ui.out, "\x1b[1A")
	}
	_, _ = io.WriteString(ui.out, "\r")
	ui.headerLines = 0
	ui.promptLines = 0
	ui.promptCursorRow = 0
	ui.promptHintLines = 0
	ui.editing = false
}

func (ui *terminalUI) notify(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	message = ui.decorateMessage(message)
	ui.writeWrapped(message)
	if !strings.HasSuffix(message, "\n") {
		ui.writeWrapped("\n")
	}
}

func (ui *terminalUI) ShowStatus(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.refreshSize()
	ui.clearPromptLocked()
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	if text == "" {
		ui.clearStatusLocked()
		return
	}
	runes := []rune(text)
	if ui.width > 4 && len(runes) >= ui.width {
		text = string(runes[:ui.width-4]) + "..."
	}
	_, _ = io.WriteString(ui.out, "\r\x1b[2K")
	_, _ = io.WriteString(ui.out, ui.Styled("dim", text))
	ui.statusVisible = true
	ui.outputColumn = 0
}

func (ui *terminalUI) ClearStatus() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
}

func (ui *terminalUI) Styled(kind, text string) string {
	if text == "" {
		return ""
	}
	switch kind {
	case "dim":
		return "\x1b[2m" + text + "\x1b[0m"
	case "warn":
		return "\x1b[33m" + text + "\x1b[0m"
	case "error":
		return "\x1b[31m" + text + "\x1b[0m"
	case "command":
		return "\x1b[36m" + text + "\x1b[0m"
	default:
		return text
	}
}

func (ui *terminalUI) decorateMessage(message string) string {
	trimmed := strings.TrimSpace(message)
	switch {
	case strings.HasPrefix(trimmed, "error:"):
		return ui.Styled("error", message)
	case strings.HasPrefix(trimmed, "warning:"), strings.HasPrefix(trimmed, "No provider configured."), strings.HasPrefix(trimmed, "Provider "):
		return ui.Styled("warn", message)
	default:
		return message
	}
}

func (ui *terminalUI) blankLine() {
	ui.writeWrapped("\n")
}

func (ui *terminalUI) clearStatusLocked() {
	if !ui.statusVisible {
		return
	}
	_, _ = io.WriteString(ui.out, "\r\x1b[2K")
	ui.statusVisible = false
	ui.outputColumn = 0
}

func (ui *terminalUI) ClearScreen() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	ui.clearPromptLocked()
	_, _ = io.WriteString(ui.out, "\x1b[2J\x1b[H")
	ui.outputColumn = 0
	ui.headerLines = 0
	ui.promptLines = 0
	ui.promptCursorRow = 0
	ui.editing = false
}

func (ui *terminalUI) commitInput(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	if ui.editing {
		ui.clearPromptLocked()
	}
	ui.refreshSize()
	lines := wrapPromptLines(ui.prompt, text, ui.width)
	for i, line := range lines {
		if i > 0 {
			_, _ = io.WriteString(ui.out, "\r\n")
		}
		_, _ = io.WriteString(ui.out, line)
	}
	_, _ = io.WriteString(ui.out, "\r\n")
	ui.outputColumn = 0
}

func (ui *terminalUI) bell() {
	_, _ = io.WriteString(ui.out, "\a")
}

func (ui *terminalUI) Write(p []byte) (int, error) {
	ui.writeWrapped(string(p))
	return len(p), nil
}

func (ui *terminalUI) writeWrapped(s string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	if ui.editing {
		ui.clearPromptLocked()
	}
	ui.refreshSize()
	runes := []rune(s)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch r {
		case 27:
			consumed := ui.writeANSIEscapeLocked(runes[i:])
			i += consumed
		case '\r':
			_, _ = io.WriteString(ui.out, "\r")
			ui.outputColumn = 0
			i++
		case '\n':
			_, _ = io.WriteString(ui.out, "\r\n")
			ui.outputColumn = 0
			i++
		case '\t', ' ':
			j := i
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			next := j
			for next < len(runes) && !isWrapBoundaryRune(runes[next]) {
				next++
			}
			ui.writeSpaceBeforeWordLocked(next - j)
			i = j
		default:
			j := i
			for j < len(runes) && !isWrapBoundaryRune(runes[j]) {
				j++
			}
			ui.writeWordLocked(string(runes[i:j]), j-i)
			i = j
		}
	}
}

func isWrapBoundaryRune(r rune) bool {
	return r == 27 || r == '\r' || r == '\n' || r == '\t' || r == ' '
}

func (ui *terminalUI) writeANSIEscapeLocked(runes []rune) int {
	if len(runes) == 0 {
		return 0
	}
	_, _ = io.WriteString(ui.out, string(runes[0]))
	state := 1
	for i := 1; i < len(runes); i++ {
		r := runes[i]
		_, _ = io.WriteString(ui.out, string(r))
		switch state {
		case 1:
			if r == '[' {
				state = 2
			} else if ansiEscapeFinal(r) {
				return i + 1
			}
		case 2:
			if ansiEscapeFinal(r) {
				return i + 1
			}
		}
	}
	return len(runes)
}

func (ui *terminalUI) writeSpaceBeforeWordLocked(nextWordWidth int) {
	if ui.width <= 0 {
		ui.width = 80
	}
	if ui.outputColumn == 0 {
		return
	}
	if nextWordWidth > 0 && nextWordWidth <= ui.width && ui.outputColumn+1+nextWordWidth > ui.width {
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.outputColumn = 0
		return
	}
	if ui.outputColumn+1 >= ui.width {
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.outputColumn = 0
		return
	}
	ui.writeRuneLocked(' ')
}

func (ui *terminalUI) writeWordLocked(word string, width int) {
	if ui.width <= 0 {
		ui.width = 80
	}
	if width <= 0 {
		return
	}
	if width > ui.width {
		for _, r := range word {
			ui.writeRuneLocked(r)
		}
		return
	}
	if ui.outputColumn > 0 && ui.outputColumn+width > ui.width {
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.outputColumn = 0
	}
	_, _ = io.WriteString(ui.out, word)
	ui.outputColumn += width
}

func (ui *terminalUI) writeRuneLocked(r rune) {
	if ui.width <= 0 {
		ui.width = 80
	}
	if ui.outputColumn >= ui.width {
		_, _ = io.WriteString(ui.out, "\r\n")
		ui.outputColumn = 0
	}
	_, _ = io.WriteString(ui.out, string(r))
	ui.outputColumn++
}
