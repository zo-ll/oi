package chat

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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
	mu              sync.Mutex
}

type clipboard struct {
	out *os.File
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
		in:        inFile,
		out:       outFile,
		width:     terminalWidth(inFile),
		prompt:    "> ",
		resizeCh:  make(chan os.Signal, 1),
		clipboard: clipboard{out: outFile},
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

func (ui *terminalUI) commitInput(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
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

func (ui *terminalUI) readMessage(lastAssistant string) (string, error) {
	var buf []rune
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
			ui.clearPrompt()
			return text, nil
		case 3:
			ui.clearPrompt()
			return "", io.EOF
		case 4:
			if len(buf) == 0 {
				ui.clearPrompt()
				return "", io.EOF
			}
		case 8, 127:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				ui.renderPrompt(string(buf))
			} else {
				ui.bell()
			}
		case 11:
			buf = append(buf, '\n')
			ui.renderPrompt(string(buf))
		case 22:
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
			text, handled, err := ui.readEscapeSequence()
			if err != nil {
				return "", err
			}
			if !handled {
				ui.bell()
				continue
			}
			if text != "" {
				buf = append(buf, []rune(normalizePastedText(text))...)
				ui.renderPrompt(string(buf))
			}
		default:
			if b >= 32 || b == '\t' {
				buf = append(buf, rune(b))
				ui.renderPrompt(string(buf))
			}
		}
	}
}

func (ui *terminalUI) readEscapeSequence() (string, bool, error) {
	first, err := readByte(ui.in)
	if err != nil {
		return "", false, err
	}
	if first != '[' {
		return "", false, nil
	}
	var seq bytes.Buffer
	seq.WriteByte(first)
	for seq.Len() < 16 {
		b, err := readByte(ui.in)
		if err != nil {
			return "", false, err
		}
		seq.WriteByte(b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	s := seq.String()
	switch s {
	case "[A", "[B", "[C", "[D", "[H", "[F", "[1~", "[4~", "[3~":
		return "", true, nil
	case "[200~":
		text, err := readBracketedPaste(ui.in)
		return text, true, err
	default:
		return "", false, nil
	}
}

func (ui *terminalUI) Write(p []byte) (int, error) {
	ui.writeWrapped(string(p))
	return len(p), nil
}

func (ui *terminalUI) writeWrapped(s string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
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

func ansiEscapeFinal(r rune) bool {
	return r >= '@' && r <= '~' && r != '['
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

func (c clipboard) Read() (string, error) {
	if path, args, ok := clipboardReadCommand(); ok {
		cmd := exec.Command(path, args...)
		data, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
	}
	return "", fmt.Errorf("clipboard paste unavailable")
}

func (c clipboard) Write(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if c.out != nil {
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		seq := "\x1b]52;c;" + encoded + "\x07"
		if strings.Contains(strings.ToLower(os.Getenv("TERM")), "tmux") || os.Getenv("TMUX") != "" {
			seq = "\x1bPtmux;\x1b" + seq + "\x1b\\"
		}
		if _, err := io.WriteString(c.out, seq); err == nil {
			return nil
		}
	}
	if path, args, input, ok := clipboardWriteCommand(text); ok {
		cmd := exec.Command(path, args...)
		cmd.Stdin = strings.NewReader(input)
		return cmd.Run()
	}
	return fmt.Errorf("clipboard copy unavailable")
}

func clipboardReadCommand() (string, []string, bool) {
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{"wl-paste", []string{"--no-newline"}},
		{"xclip", []string{"-selection", "clipboard", "-o"}},
		{"xsel", []string{"--clipboard", "--output"}},
		{"pbpaste", nil},
		{"powershell.exe", []string{"-NoProfile", "-Command", "Get-Clipboard -Raw"}},
	} {
		if path, err := exec.LookPath(candidate.name); err == nil {
			return path, candidate.args, true
		}
	}
	return "", nil, false
}

func clipboardWriteCommand(text string) (string, []string, string, bool) {
	for _, candidate := range []struct {
		name  string
		args  []string
		input string
	}{
		{"wl-copy", nil, text},
		{"xclip", []string{"-selection", "clipboard"}, text},
		{"xsel", []string{"--clipboard", "--input"}, text},
		{"pbcopy", nil, text},
		{"clip.exe", nil, text},
		{"powershell.exe", []string{"-NoProfile", "-Command", "$input | Set-Clipboard"}, text},
	} {
		if path, err := exec.LookPath(candidate.name); err == nil {
			return path, candidate.args, candidate.input, true
		}
	}
	return "", nil, "", false
}

func readByte(f *os.File) (byte, error) {
	var buf [1]byte
	_, err := f.Read(buf[:])
	if err != nil {
		return 0, err
	}
	return buf[0], nil
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

func normalizePastedText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
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
