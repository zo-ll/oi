package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/retrieval"
	"github.com/zo-ll/oi/internal/tool"
)

type toolVerbosity string

const (
	toolVerbosityOff    toolVerbosity = "off"
	toolVerbosityErrors toolVerbosity = "errors"
	toolVerbosityOn     toolVerbosity = "on"
)

func configureChatRuntime(rt *agent.Runtime, out io.Writer, tools toolVerbosity) {
	if rt == nil {
		return
	}
	indicator := newActivityIndicator(out)
	rt.OnRetrieve = nil
	rt.OnModelStart = indicator.StartThinking
	rt.OnModelStop = indicator.Clear
	rt.OnToolStart = nil
	rt.OnToolResult = nil
	if tools == toolVerbosityOff {
		return
	}
	rt.OnToolStart = func(call tool.Call) {
		indicator.Show(toolActivityLabel(call))
		if tools != toolVerbosityOn {
			return
		}
		fmt.Fprintln(out, styleText(out, "dim", formatToolStartLine(call)))
	}
	rt.OnToolResult = func(call tool.Call, result tool.Result) {
		indicator.Clear()
		if tools == toolVerbosityErrors && result.OK {
			return
		}
		kind := "dim"
		if !result.OK {
			kind = "warn"
		}
		fmt.Fprintln(out, styleText(out, kind, formatToolResultLine(call, result)))
	}
}

func summarizeToolArgs(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	s := string(b)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

func summarizeToolOutput(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 100 {
		s = s[:97] + "..."
	}
	return s
}

func toolArgString(raw []byte, key string) string {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func toolArgPath(raw []byte) string {
	if path := toolArgString(raw, "path"); path != "" {
		return path
	}
	return toolArgString(raw, "file")
}

func quoteShort(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 48 {
		s = s[:45] + "..."
	}
	return fmt.Sprintf("%q", s)
}

func toolTarget(call tool.Call, result *tool.Result) string {
	switch call.Name {
	case "read_file", "write_file", "replace_in_file", "list_dir":
		if result != nil && result.Meta["path"] != "" {
			return result.Meta["path"]
		}
		return toolArgPath(call.Args)
	case "find_files", "grep":
		pattern := quoteShort(toolArgString(call.Args, "pattern"))
		path := toolArgPath(call.Args)
		if path == "" {
			path = "."
		}
		if pattern == "" {
			return path
		}
		if path == "." {
			return pattern
		}
		return pattern + " in " + path
	case "run_command":
		return strings.TrimSpace(toolArgString(call.Args, "command"))
	default:
		return summarizeToolArgs(call.Args)
	}
}

func toolVerb(call tool.Call) string {
	switch call.Name {
	case "read_file":
		return "read"
	case "list_dir":
		return "list"
	case "find_files":
		return "find"
	case "grep":
		return "grep"
	case "run_command":
		return "run"
	case "write_file":
		return "write"
	case "replace_in_file":
		return "edit"
	default:
		return call.Name
	}
}

func toolProgressiveVerb(call tool.Call) string {
	switch call.Name {
	case "read_file":
		return "reading"
	case "list_dir":
		return "listing"
	case "find_files":
		return "finding"
	case "grep":
		return "grepping"
	case "run_command":
		return "running"
	case "write_file":
		return "writing"
	case "replace_in_file":
		return "editing"
	default:
		return "running"
	}
}

func toolActivityLabel(call tool.Call) string {
	line := toolProgressiveVerb(call)
	if target := toolTarget(call, nil); target != "" {
		line += " " + target
	}
	return line
}

func formatToolStartLine(call tool.Call) string {
	line := "  |> " + toolVerb(call)
	if target := toolTarget(call, nil); target != "" {
		line += " " + target
	}
	return line
}

func formatToolResultLine(call tool.Call, result tool.Result) string {
	verb := toolVerb(call)
	target := toolTarget(call, &result)
	if !result.OK {
		line := "  !  " + verb + " failed"
		if target != "" {
			line += " " + target
		}
		if result.Error != "" {
			line += ": " + result.Error
		}
		return line
	}
	line := "  |  " + verb + " ok"
	if target != "" {
		line += " " + target
	}
	if result.Meta["truncated"] == "true" {
		line += " (truncated)"
	} else if verb == call.Name {
		if text := summarizeToolOutput(result.Output); text != "" {
			line += ": " + text
		}
	}
	return line
}

type styledWriter interface {
	Styled(kind, text string) string
}

type clearer interface {
	ClearScreen()
}

type statusWriter interface {
	ShowStatus(text string)
	ClearStatus()
}

func styleText(out io.Writer, kind, text string) string {
	if sw, ok := out.(styledWriter); ok {
		return sw.Styled(kind, text)
	}
	return text
}

func printHelpLine(out io.Writer, left, right string) {
	fmt.Fprintf(out, "%-22s %s\n", styleText(out, "command", left), right)
}

func clearScreen(out io.Writer) {
	if c, ok := out.(clearer); ok {
		c.ClearScreen()
		return
	}
	fmt.Fprint(out, "\x1b[2J\x1b[H")
}

func formatHeader(model, root string, contextWindow int, usage provider.Usage, think string, thinkSupported bool) string {
	header := fmt.Sprintf("oi · %s", valueOr(model, "(none)"))
	thinkLabel := valueOr(think, "default")
	if !thinkSupported {
		thinkLabel = "n/a"
	}
	header += " · " + thinkLabel
	if ctx := formatHeaderContextUsage(contextWindow, usage); ctx != "" {
		header += " · " + ctx
	}
	header += " · " + shortenPath(root)
	return header
}

func formatHeaderContextUsage(window int, usage provider.Usage) string {
	if window <= 0 {
		return ""
	}
	if usage.InputTokens <= 0 {
		return "0 / " + formatCount(window) + " (0%)"
	}
	pct := usage.InputTokens * 100 / window
	return fmt.Sprintf("%s / %s (%d%%)", formatCount(usage.InputTokens), formatCount(window), pct)
}

func lookupContextWindow(p provider.Provider, model string) int {
	return lookupModelInfo(p, model).ContextWindow
}

func supportedThinkingLevels(model provider.Model) []string {
	if !model.SupportsThinking {
		return []string{"off"}
	}
	levels := []string{"off", "low", "medium", "high"}
	if len(model.SupportedThinkingLevels) > 0 {
		levels = append([]string(nil), model.SupportedThinkingLevels...)
	}
	if !containsString(levels, "off") {
		levels = append([]string{"off"}, levels...)
	}
	return levels
}

func thinkingValue(model provider.Model, level string) string {
	if model.ThinkingLevelValues != nil {
		if value, ok := model.ThinkingLevelValues[level]; ok {
			return value
		}
	}
	return level
}

func clampThinkingLevel(model provider.Model, level string) string {
	levels := supportedThinkingLevels(model)
	if len(levels) == 0 {
		return "off"
	}
	if level == "" {
		level = "off"
	}
	if containsString(levels, level) {
		return level
	}
	for _, fallback := range []string{"medium", "high", "low", "off"} {
		if containsString(levels, fallback) {
			return fallback
		}
	}
	return levels[0]
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func lookupModelInfo(p provider.Provider, model string) provider.Model {
	if p == nil || strings.TrimSpace(model) == "" {
		return provider.Model{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return provider.Model{}
	}
	for _, item := range models {
		if item.ID == model {
			return item
		}
	}
	return provider.Model{}
}

func formatRetrievalNotice(notice retrieval.Notice) string {
	if notice.SnippetCount <= 0 {
		return ""
	}
	files := "files"
	if notice.FileCount == 1 {
		files = "file"
	}
	return fmt.Sprintf("retrieved %d snippets from %d %s", notice.SnippetCount, notice.FileCount, files)
}

func formatContextUsage(window int, usage provider.Usage) string {
	if window <= 0 || usage.InputTokens <= 0 {
		return ""
	}
	pct := usage.InputTokens * 100 / window
	return fmt.Sprintf("ctx %s / %s (%d%%)", formatCount(usage.InputTokens), formatCount(window), pct)
}

func formatCount(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func shortenPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "(none)"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if path == home {
			return "~"
		}
		prefix := home + string(filepath.Separator)
		if strings.HasPrefix(path, prefix) {
			return "~" + string(filepath.Separator) + strings.TrimPrefix(path, prefix)
		}
	}
	return path
}

var displayReplacer = strings.NewReplacer(
	"**", "",
	"__", "",
	"`", "",
	"\u00a0", " ",
	"â€™", "’",
	"â€œ", "“",
	"â€", "”",
	"â€˜", "‘",
	"â€”", "—",
	"â€“", "–",
	"â€¦", "…",
	"ÔÇÖ", "’",
	"ÔÇ£", "“",
	"ÔÇ¥", "”",
	"ÔÇö", "—",
	"ÔÇô", "–",
)

type outputFormatter struct {
	tail string
}

func (f *outputFormatter) Push(text string) string {
	f.tail += text
	runes := []rune(f.tail)
	if len(runes) <= 6 {
		return ""
	}
	flush := string(runes[:len(runes)-6])
	f.tail = string(runes[len(runes)-6:])
	return cleanDisplayText(flush)
}

func (f *outputFormatter) Flush() string {
	out := cleanDisplayText(f.tail)
	f.tail = ""
	return out
}

func cleanDisplayText(text string) string {
	text = displayReplacer.Replace(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# "):
			lines[i] = strings.Replace(line, "# ", "", 1)
		case strings.HasPrefix(trimmed, "## "):
			lines[i] = strings.Replace(line, "## ", "", 1)
		case strings.HasPrefix(trimmed, "### "):
			lines[i] = strings.Replace(line, "### ", "", 1)
		}
	}
	return strings.Join(lines, "\n")
}

func parseToolVerbosity(arg string) (toolVerbosity, error) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "", "errors":
		return toolVerbosityErrors, nil
	case "off":
		return toolVerbosityOff, nil
	case "on":
		return toolVerbosityOn, nil
	default:
		return "", fmt.Errorf("usage: /tools [off|errors|on]")
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

type activityIndicator struct {
	sink statusWriter
	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

func newActivityIndicator(out io.Writer) *activityIndicator {
	sink, _ := out.(statusWriter)
	return &activityIndicator{sink: sink}
}

func (a *activityIndicator) StartThinking() {
	a.startSpinner("thinking")
}

func (a *activityIndicator) Show(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sink == nil {
		return
	}
	a.stopLocked(false)
	a.sink.ShowStatus(text)
}

func (a *activityIndicator) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopLocked(true)
}

func (a *activityIndicator) startSpinner(label string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sink == nil {
		return
	}
	a.stopLocked(false)
	stop := make(chan struct{})
	done := make(chan struct{})
	a.stop = stop
	a.done = done
	go func() {
		defer close(done)
		frames := []string{"|", "/", "-", "\\"}
		i := 0
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			a.sink.ShowStatus(fmt.Sprintf("%s %s", label, frames[i%len(frames)]))
			i++
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (a *activityIndicator) stopLocked(clear bool) {
	stop := a.stop
	done := a.done
	a.stop = nil
	a.done = nil
	if stop != nil {
		close(stop)
		a.mu.Unlock()
		<-done
		a.mu.Lock()
	}
	if clear && a.sink != nil {
		a.sink.ClearStatus()
	}
}
