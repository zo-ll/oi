package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/tool"
)

func configureChatRuntime(rt *agent.Runtime, out io.Writer) {
	if rt == nil {
		return
	}
	rt.OnToolStart = func(call tool.Call) {
		line := fmt.Sprintf("  tool:start %s %s", call.Name, summarizeToolArgs(call.Args))
		fmt.Fprintln(out, styleText(out, "dim", line))
	}
	rt.OnToolResult = func(call tool.Call, result tool.Result) {
		status := "ok"
		if !result.OK {
			status = "error"
		}
		line := fmt.Sprintf("  tool:%s %s", status, call.Name)
		if result.Error != "" {
			line += ": " + result.Error
		} else if text := summarizeToolOutput(result.Output); text != "" {
			line += ": " + text
		}
		kind := "dim"
		if !result.OK {
			kind = "warn"
		}
		fmt.Fprintln(out, styleText(out, kind, line))
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

type styledWriter interface {
	Styled(kind, text string) string
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

func formatHeader(model, root string) string {
	return fmt.Sprintf("oi · %s · %s", valueOr(model, "(none)"), shortenPath(root))
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

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func startThinkingIndicator(out io.Writer) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		frames := []string{"thinking   ", "thinking.  ", "thinking.. ", "thinking..."}
		i := 0
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				fmt.Fprint(out, "\r            \r")
				return
			case <-ticker.C:
				fmt.Fprintf(out, "\r%s", frames[i%len(frames)])
				i++
			}
		}
	}()
	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}
}
