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
	outputTail      string
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
	promptText      string
	promptCursor    int
	statusText      string
	transcriptLines []string
	streamThinking  string
	streamAnswer    string
	streamActive    bool
	streamDirty     bool
	streamLines     int
	streamMaxLines  int
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
	ui.promptText = text
	ui.promptCursor = cursor
	ui.editing = true
	ui.redrawLocked()
}

func (ui *terminalUI) clearPrompt() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearPromptLocked()
	ui.redrawLocked()
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

func (ui *terminalUI) streamRenderLinesLocked() []string {
	width := ui.width
	if width <= 0 {
		width = 80
	}
	var lines []string
	if thinking := cleanDisplayText(ui.streamThinking); thinking != "" {
		for _, line := range wrapText(thinking, width) {
			lines = append(lines, ui.Styled("dim", line))
		}
		if strings.TrimSpace(ui.streamAnswer) != "" {
			lines = append(lines, "")
		}
	}
	if answer := cleanDisplayText(ui.streamAnswer); answer != "" {
		lines = append(lines, wrapText(answer, width)...)
	}
	return lines
}

func (ui *terminalUI) appendTranscriptMessageLocked(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	width := ui.width
	if width <= 0 {
		width = 80
	}
	kind := ""
	trimmed := strings.TrimSpace(message)
	switch {
	case strings.HasPrefix(trimmed, "error:"):
		kind = "error"
	case strings.HasPrefix(trimmed, "warning:"), strings.HasPrefix(trimmed, "No provider configured."), strings.HasPrefix(trimmed, "Provider "):
		kind = "warn"
	}
	for _, line := range wrapText(message, width) {
		if kind != "" {
			ui.transcriptLines = append(ui.transcriptLines, ui.Styled(kind, line))
		} else {
			ui.transcriptLines = append(ui.transcriptLines, line)
		}
	}
}

func (ui *terminalUI) redrawLocked() {
	ui.refreshSize()
	headerLines := ui.wrapHeaderLinesLocked()
	hintLines := ui.wrapHintLinesLocked()
	streamLines := []string(nil)
	if ui.streamActive || strings.TrimSpace(ui.streamThinking) != "" || strings.TrimSpace(ui.streamAnswer) != "" {
		streamLines = ui.streamRenderLinesLocked()
	}
	statusLines := []string(nil)
	if strings.TrimSpace(ui.statusText) != "" {
		statusLines = []string{ui.Styled("dim", ui.statusText)}
	}
	promptLines := []string(nil)
	row, col := 0, 0
	if ui.editing {
		promptLines = wrapPromptLines(ui.prompt, ui.promptText, ui.width)
		if len(promptLines) == 0 {
			promptLines = []string{ui.prompt}
		}
		row, col = promptCursorPosition(ui.prompt, ui.promptText, ui.width, ui.promptCursor)
		ui.promptLines = len(promptLines)
		ui.promptCursorRow = row
	} else {
		ui.promptLines = 0
		ui.promptCursorRow = 0
	}
	var frame []string
	frame = append(frame, headerLines...)
	frame = append(frame, ui.transcriptLines...)
	frame = append(frame, streamLines...)
	frame = append(frame, statusLines...)
	frame = append(frame, hintLines...)
	frame = append(frame, promptLines...)
	_, _ = io.WriteString(ui.out, "\x1b[2J\x1b[H")
	for i, line := range frame {
		if i > 0 {
			_, _ = io.WriteString(ui.out, "\r\n")
		}
		_, _ = io.WriteString(ui.out, line)
	}
	if ui.editing && len(promptLines) > 0 {
		if up := len(promptLines) - 1 - row; up > 0 {
			_, _ = io.WriteString(ui.out, fmt.Sprintf("\x1b[%dA", up))
		}
		_, _ = io.WriteString(ui.out, "\r")
		if col > 0 {
			_, _ = io.WriteString(ui.out, fmt.Sprintf("\x1b[%dC", col))
		}
	}
}

func (ui *terminalUI) clearPromptLocked() {
	ui.promptText = ""
	ui.promptCursor = 0
	ui.promptLines = 0
	ui.promptCursorRow = 0
	ui.editing = false
}

func (ui *terminalUI) notify(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.appendTranscriptMessageLocked(message)
	ui.redrawLocked()
}

func (ui *terminalUI) ShowStatus(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.refreshSize()
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	if text == "" {
		ui.clearStatusLocked()
		ui.redrawLocked()
		return
	}
	runes := []rune(text)
	if ui.width > 4 && len(runes) >= ui.width {
		text = string(runes[:ui.width-4]) + "..."
	}
	ui.statusText = text
	ui.statusVisible = true
	ui.redrawLocked()
}

func (ui *terminalUI) ClearStatus() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	ui.redrawLocked()
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
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.transcriptLines = append(ui.transcriptLines, "")
	ui.redrawLocked()
}

func (ui *terminalUI) clearStatusLocked() {
	ui.statusVisible = false
	ui.statusText = ""
}

func (ui *terminalUI) ClearScreen() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	ui.clearPromptLocked()
	ui.transcriptLines = nil
	ui.streamThinking = ""
	ui.streamAnswer = ""
	ui.streamActive = false
	ui.streamDirty = false
	ui.redrawLocked()
}

func (ui *terminalUI) commitInput(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	ui.refreshSize()
	ui.transcriptLines = append(ui.transcriptLines, wrapPromptLines(ui.prompt, text, ui.width)...)
	ui.transcriptLines = append(ui.transcriptLines, "")
	ui.clearPromptLocked()
	ui.redrawLocked()
}

func (ui *terminalUI) bell() {
	_, _ = io.WriteString(ui.out, "\a")
}

func (ui *terminalUI) Write(p []byte) (int, error) {
	text := string(p)
	if strings.TrimSpace(text) == "" {
		return len(p), nil
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.appendTranscriptMessageLocked(strings.TrimRight(text, "\n"))
	ui.redrawLocked()
	return len(p), nil
}

func (ui *terminalUI) startAssistantResponse() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.streamThinking = ""
	ui.streamAnswer = ""
	ui.streamActive = true
	ui.streamDirty = false
	ui.redrawLocked()
}

func (ui *terminalUI) writeStreamSegments(segs []responseSegment) {
	if len(segs) == 0 {
		return
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	for _, seg := range segs {
		if seg.reasoning {
			ui.streamThinking += seg.text
		} else {
			ui.streamAnswer += seg.text
		}
	}
	ui.streamDirty = true
}

func (ui *terminalUI) flushStream() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if !ui.streamActive || !ui.streamDirty {
		return
	}
	ui.streamDirty = false
	ui.redrawLocked()
}

func (ui *terminalUI) finishAssistantResponse() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.transcriptLines = append(ui.transcriptLines, ui.streamRenderLinesLocked()...)
	ui.streamThinking = ""
	ui.streamAnswer = ""
	ui.streamActive = false
	ui.streamDirty = false
	ui.redrawLocked()
}

func wrapText(text string, width int) []string {
	var out []string
	for _, part := range strings.Split(text, "\n") {
		if part == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapLine(part, width)...)
	}
	return out
}

func (ui *terminalUI) writeWrapped(s string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearStatusLocked()
	if ui.editing {
		ui.clearPromptLocked()
	}
	ui.refreshSize()
	s = ui.outputTail + s
	ui.outputTail = ""
	if keep, rest := splitTrailingWord(s); keep != "" {
		ui.outputTail = keep
		s = rest
	}
	if s == "" {
		return
	}
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

func splitTrailingWord(s string) (tail, body string) {
	if s == "" {
		return "", ""
	}
	runes := []rune(s)
	last := runes[len(runes)-1]
	if isWrapBoundaryRune(last) {
		return "", s
	}
	for i := len(runes) - 1; i >= 0; i-- {
		if isWrapBoundaryRune(runes[i]) {
			if runes[i] == ' ' || runes[i] == '\t' {
				return string(runes[i:]), string(runes[:i])
			}
			return string(runes[i+1:]), string(runes[:i+1])
		}
	}
	return s, ""
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
