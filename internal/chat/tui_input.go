package chat

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/zo-ll/tide"
)

func (a *tuiApp) nextByte() (byte, error) {
	for {
		b, err := a.readRawByte()
		if err != nil {
			return 0, err
		}
		// Collapse CRLF to a single CR so the LF does not leak into the
		// next overlay as a spurious Enter.
		if b == '\n' && a.lastByteCR {
			a.lastByteCR = false
			continue
		}
		a.lastByteCR = b == '\r'
		return b, nil
	}
}

// readRawByte is the raw channel pump behind nextByte.
func (a *tuiApp) readRawByte() (byte, error) {
	for {
		select {
		case fn := <-a.events:
			if fn != nil {
				fn()
			}
		case req := <-a.approvals:
			a.approval = &req
			a.renderApprovalOverlay(req.action, req.target)
		case <-a.errCh:
			return 0, io.EOF
		case b := <-a.inputCh:
			if a.handleApprovalInput(b) {
				continue
			}
			return b, nil
		}
	}
}

func (a *tuiApp) handleApprovalInput(b byte) bool {
	if a.approval == nil {
		return false
	}
	switch b {
	case 'y', 'Y':
		a.approval.resp <- true
		a.approval = nil
		a.render()
	case 3, 4:
		// Ctrl-C / Ctrl-D deny and request full app exit.
		a.approval.resp <- false
		a.approval = nil
		a.quitRequested = true
		a.render()
	case 'n', 'N', 27:
		a.approval.resp <- false
		a.approval = nil
		if b == 27 {
			// ESC cancels the approval; drain the rest of the escape/mouse
			// sequence so its bytes do not leak into the typed input buffer.
			_, _, _ = tide.ReadEscape(a.nextByte)
		}
		a.render()
	default:
		a.renderApprovalOverlay(a.approval.action, a.approval.target)
	}
	return true
}

func (a *tuiApp) addHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(a.history) > 0 && a.history[0] == line {
		a.historyIndex = -1
		a.historyDraft = nil
		return
	}
	a.history = append([]string{line}, a.history...)
	if len(a.history) > 200 {
		a.history = a.history[:200]
	}
	a.historyIndex = -1
	a.historyDraft = nil
}

func (a *tuiApp) historyPrev() {
	if len(a.history) == 0 {
		return
	}
	if a.historyIndex == -1 {
		a.historyDraft = append([]rune(nil), a.input...)
		a.historyIndex = 0
	} else if a.historyIndex+1 < len(a.history) {
		a.historyIndex++
	}
	a.input = []rune(a.history[a.historyIndex])
	a.cursor = len(a.input)
	a.hintIdx = 0
}

func (a *tuiApp) historyNext() {
	if a.historyIndex == -1 {
		return
	}
	if a.historyIndex > 0 {
		a.historyIndex--
		a.input = []rune(a.history[a.historyIndex])
	} else {
		a.historyIndex = -1
		a.input = append([]rune(nil), a.historyDraft...)
		a.historyDraft = nil
	}
	a.cursor = len(a.input)
	a.hintIdx = 0
}

// handleEscape dispatches an escape/cursor-key/mouse sequence using tide's
// normalized ReadEscape. Arrow keys dual-purpose: when slash-command hints are
// visible they navigate hints, otherwise up/down walk prompt history and the
// mouse wheel scrolls the transcript.
func (a *tuiApp) handleEscape() {
	kind, _, err := tide.ReadEscape(a.nextByte)
	if err != nil {
		return
	}
	switch kind {
	case tide.EscPageUp:
		a.scrollPage(1)
	case tide.EscPageDown:
		a.scrollPage(-1)
	case tide.EscUp:
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx > 0 {
				a.hintIdx--
			}
		} else {
			a.historyPrev()
		}
	case tide.EscDown:
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx+1 < len(hints) {
				a.hintIdx++
			}
		} else {
			a.historyNext()
		}
	case tide.EscScrollUp:
		a.scrollLines(3)
	case tide.EscScrollDn:
		a.scrollLines(-3)
	case tide.EscRight:
		if a.cursor < len(a.input) {
			a.cursor++
		}
	case tide.EscLeft:
		if a.cursor > 0 {
			a.cursor--
		}
	case tide.EscHome:
		a.cursor = 0
	case tide.EscEnd:
		a.cursor = len(a.input)
	case tide.EscMouse, tide.EscCancel:
		// no-op: a plain click or bare ESC during input
	}
}

func (a *tuiApp) scrollLines(delta int) {
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
}

func (a *tuiApp) scrollPage(delta int) {
	size := a.term.Size()
	step := size.Rows - 6
	if step < 1 {
		step = 1
	}
	a.scrollLines(delta * step)
}

var chatCommandList = []string{
	"/help", "/login", "/model", "/stream", "/think", "/tools", "/autosave",
	"/status", "/new", "/save", "/session", "/compact", "/clear", "/exit",
}

func chatCommands() []string {
	return append([]string(nil), chatCommandList...)
}

func filterByPrefix(items []string, prefix string) []string {
	prefix = strings.ToLower(prefix)
	var out []string
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), "/") && strings.HasPrefix(strings.ToLower(strings.TrimPrefix(item, "/")), prefix) {
			out = append(out, item)
		}
	}
	return out
}

func exactMatch(current string, matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	_, _, token, ok := trailingToken(current)
	if !ok {
		return ""
	}
	query := strings.TrimSpace(strings.TrimPrefix(token, strings.ToLower(string(token[:1]))))
	query = strings.ToLower(query)
	if query == "" {
		return ""
	}
	var exact string
	count := 0
	for _, match := range matches {
		lower := strings.ToLower(match)
		if !strings.HasPrefix(token, "@") {
			if lower == strings.ToLower(token) {
				exact = match
				count++
			}
		} else {
			slashed := strings.ToLower(filepath.ToSlash(match))
			if slashed == query {
				exact = match
				count++
			}
			if strings.ToLower(filepath.Base(slashed)) == query {
				if exact == "" {
					exact = match
				}
			}
		}
	}
	if count == 1 && exact != "" {
		return exact
	}
	return ""
}

func replaceTrailingToken(current, replacement string) string {
	start, end, _, ok := trailingToken(current)
	if !ok {
		return current
	}
	return current[:start] + replacement + current[end:]
}

func liveHint(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	if len(matches) == 1 {
		return "tab → " + matches[0]
	}
	return fmt.Sprintf("%d matches  tab→pick", len(matches))
}

func pickerHint(matches []string, index int) string {
	if len(matches) == 0 {
		return ""
	}
	if index < 0 {
		index = 0
	}
	if index >= len(matches) {
		index = len(matches) - 1
	}
	shown := matches
	if len(shown) > maxCompletionMatchesShown {
		start := index - maxCompletionMatchesShown/2
		if start < 0 {
			start = 0
		}
		if start+maxCompletionMatchesShown > len(matches) {
			start = len(matches) - maxCompletionMatchesShown
		}
		if start < 0 {
			start = 0
		}
		shown = shown[start : start+maxCompletionMatchesShown]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d matches", len(matches)))
	b.WriteString("  up/down nav  enter pick\n")
	for _, match := range shown {
		marker := "  "
		if strings.EqualFold(match, matches[index]) {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, match)
	}
	return strings.TrimRight(b.String(), "\n")
}

func filterPickerItems(items []string, query string) []string {
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

func formatCompletionMatches(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	shown := matches
	if len(shown) > maxCompletionMatchesShown {
		shown = shown[:maxCompletionMatchesShown]
	}
	var b strings.Builder
	b.WriteString("matches:")
	for i, match := range shown {
		fmt.Fprintf(&b, "\n %d. %s", i+1, match)
	}
	return b.String()
}

func trailingToken(s string) (start, end int, token string, ok bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, 0, "", false
	}
	end = len(runes)
	start = end
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	token = string(runes[start:end])
	return start, end, token, token != ""
}

func (a *tuiApp) commandHints() []string {
	text := string(a.input)
	if len(text) == 0 || text[0] != '/' {
		return nil
	}
	query := strings.TrimPrefix(text, "/")
	return filterByPrefix(chatCommands(), query)
}

func (a *tuiApp) tabComplete() {
	hints := a.commandHints()
	if len(hints) == 0 {
		return
	}
	if a.hintIdx >= len(hints) {
		a.hintIdx = 0
	}
	a.input = []rune(hints[a.hintIdx])
	a.cursor = len(a.input)
}
