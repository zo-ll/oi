package tide

// This file holds the text measurement and wrapping engine shared by the
// terminal toolkit (DisplayWidth, Wrap) and the widget renderers, plus the
// stty subprocess helpers used by Terminal. It is the only surviving logic
// from tide's original line-editor implementation; the Editor itself was
// removed in favor of the async Overlay/Input/Picker surface.

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func dim(text string) string {
	return "\x1b[2m" + text + "\x1b[0m"
}

// displayWidth returns the visible cell width of s, honoring combining marks
// and East-Asian wide/fullwidth runes.
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

func isWordSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// wrapLineFromRunes wraps runes to width display cells, preferring word
// (whitespace) boundaries and hard-wrapping long words. It backs Wrap and
// WrapPlain.
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
