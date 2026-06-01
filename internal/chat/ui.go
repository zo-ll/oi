package chat

import (
	"encoding/json"
	"fmt"
	"io"
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
		clearStatusLine(out)
		fmt.Fprintf(out, "[tool:start] %s %s\n", call.Name, summarizeToolArgs(call.Args))
	}
	rt.OnToolResult = func(call tool.Call, result tool.Result) {
		clearStatusLine(out)
		status := "ok"
		if !result.OK {
			status = "error"
		}
		fmt.Fprintf(out, "[tool:%s] %s", status, call.Name)
		if result.Error != "" {
			fmt.Fprintf(out, ": %s", result.Error)
		} else if text := summarizeToolOutput(result.Output); text != "" {
			fmt.Fprintf(out, ": %s", text)
		}
		fmt.Fprintln(out)
	}
}

func clearStatusLine(out io.Writer) {
	fmt.Fprint(out, "\r                    \r")
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
