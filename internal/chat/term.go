package chat

import (
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
	prompt          string
	promptLines     int
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
	if ui == nil {
		return
	}
	width := terminalWidth(ui.in)
	if width > 0 {
		ui.width = width
	}
}

func (ui *terminalUI) renderPrompt(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.refreshSize()
	ui.clearStatusLocked()
	ui.clearPromptLocked()
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
	ui.editing = true
}

func (ui *terminalUI) clearPrompt() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearPromptLocked()
}

func (ui *terminalUI) clearPromptLocked() {
	if ui.promptLines <= 0 {
		return
	}
	_, _ = io.WriteString(ui.out, "\r")
	for i := 0; i < ui.promptLines-1; i++ {
		_, _ = io.WriteString(ui.out, "\x1b[1A")
	}
	for i := 0; i < ui.promptLines; i++ {
		_, _ = io.WriteString(ui.out, "\r\x1b[2K")
		if i < ui.promptLines-1 {
			_, _ = io.WriteString(ui.out, "\x1b[1B")
		}
	}
	for i := 0; i < ui.promptLines-1; i++ {
		_, _ = io.WriteString(ui.out, "\x1b[1A")
	}
	_, _ = io.WriteString(ui.out, "\r")
	ui.promptLines = 0
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
	ui.promptLines = 0
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
	escapeState := 0
	for _, r := range s {
		if escapeState > 0 {
			_, _ = io.WriteString(ui.out, string(r))
			switch escapeState {
			case 1:
				if r == '[' {
					escapeState = 2
				} else if ansiEscapeFinal(r) {
					escapeState = 0
				}
			case 2:
				if ansiEscapeFinal(r) {
					escapeState = 0
				}
			}
			continue
		}
		switch r {
		case 27:
			escapeState = 1
			_, _ = io.WriteString(ui.out, string(r))
		case '\r':
			_, _ = io.WriteString(ui.out, "\r")
			ui.outputColumn = 0
		case '\n':
			_, _ = io.WriteString(ui.out, "\r\n")
			ui.outputColumn = 0
		case '\t':
			for i := 0; i < 4; i++ {
				ui.writeRuneLocked(' ')
			}
		default:
			ui.writeRuneLocked(r)
		}
	}
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
