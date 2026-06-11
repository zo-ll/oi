package chat

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type clipboard struct {
	out *os.File
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

func ansiEscapeFinal(r rune) bool {
	return r >= '@' && r <= '~' && r != '['
}

func readByteWithTimeout(f *os.File, d time.Duration) (byte, bool, error) {
	type deadlineSetter interface{ SetReadDeadline(time.Time) error }
	ds, ok := any(f).(deadlineSetter)
	if !ok {
		b, err := readByte(f)
		return b, true, err
	}
	_ = ds.SetReadDeadline(time.Now().Add(d))
	b, err := readByte(f)
	_ = ds.SetReadDeadline(time.Time{})
	if err != nil {
		if os.IsTimeout(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return b, true, nil
}

func (ui *terminalUI) readEscapeSequence() (kind, text string, handled bool, err error) {
	first, ok, err := readByteWithTimeout(ui.in, 120*time.Millisecond)
	if err != nil {
		return "", "", false, err
	}
	if !ok {
		return "esc", "", true, nil
	}
	if first != '[' {
		return "", "", false, nil
	}
	var seq bytes.Buffer
	seq.WriteByte(first)
	for seq.Len() < 16 {
		b, err := readByte(ui.in)
		if err != nil {
			return "", "", false, err
		}
		seq.WriteByte(b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	s := seq.String()
	switch s {
	case "[A":
		return "up", "", true, nil
	case "[B":
		return "down", "", true, nil
	case "[C":
		return "right", "", true, nil
	case "[D":
		return "left", "", true, nil
	case "[H", "[F", "[1~", "[4~", "[3~":
		return "nav", "", true, nil
	case "[200~":
		text, err := readBracketedPaste(ui.in)
		return "paste", text, true, err
	default:
		return "", "", false, nil
	}
}
