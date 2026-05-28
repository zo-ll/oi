package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

func runChat(args []string, in io.Reader, out io.Writer) error {
	opts, err := parseCommonOptions("chat", args)
	if err != nil {
		return err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, err := requireProvider(sel)
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(in)
	streaming := true
	autosave := true
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return err
	}
	rt := buildRuntime(cfg, sel, p, root, reader, out, logger)
	configureChatRuntime(rt, out)

	fmt.Fprintf(out, "oi chat\nprovider: %s\nmodel: %s\nworkspace: %s\n", sel.Provider, valueOr(sel.Model, "(none)"), root)
	fmt.Fprintln(out, "Type /help for commands.")

	for {
		fmt.Fprint(out, "oi> ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				fmt.Fprintln(out)
				return exitChat(reader, out, rt, sel, autosave)
			}
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, newRT, newSel, newStreaming, newAutosave, cmdErr := handleChatCommand(cfg, sel, rt, reader, out, line, streaming, autosave)
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
			} else {
				rt = newRT
				sel = newSel
				streaming = newStreaming
				autosave = newAutosave
				configureChatRuntime(rt, out)
			}
			if exit {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Agent.RequestTimeoutSeconds)*time.Second)
		if streaming {
			spinnerStop := startThinkingIndicator(out)
			startedOutput := false
			resp, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				if !startedOutput {
					spinnerStop()
					startedOutput = true
				}
				fmt.Fprint(out, delta)
			})
			spinnerStop()
			cancel()
			if runErr != nil {
				if startedOutput {
					fmt.Fprintln(out)
				}
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else {
				if !startedOutput {
					fmt.Fprintln(out, resp)
				} else {
					fmt.Fprintln(out)
				}
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
					}
				}
			}
		} else {
			resp, runErr := rt.RunOnce(ctx, line)
			cancel()
			if runErr != nil {
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else {
				fmt.Fprintln(out, resp)
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
					}
				}
			}
		}
		if err == io.EOF {
			return exitChat(reader, out, rt, sel, autosave)
		}
	}
}

func handleChatCommand(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, line string, streaming bool, autosave bool) (bool, *agent.Runtime, config.Selection, bool, bool, error) {
	fields := strings.Fields(line)
	cmd := fields[0]
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	switch cmd {
	case "/help":
		fmt.Fprintln(out, "/help                show commands")
		fmt.Fprintln(out, "/provider [name]     show or set provider")
		fmt.Fprintln(out, "/model [name]        show models or set model")
		fmt.Fprintln(out, "/stream [on|off]     show or set streaming mode")
		fmt.Fprintln(out, "/autosave [on|off]   show or set autosave mode")
		fmt.Fprintln(out, "/new                 start a new session")
		fmt.Fprintln(out, "/sessions            list saved sessions")
		fmt.Fprintln(out, "/save [name]         save current session")
		fmt.Fprintln(out, "/load <name|path>    load a saved session")
		fmt.Fprintln(out, "/exit                exit chat")
		return false, rt, sel, streaming, autosave, nil
	case "/exit", "/quit":
		if err := exitChat(reader, out, rt, sel, autosave); err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		return true, rt, sel, streaming, autosave, nil
	case "/new":
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		rt.Session = session.New(sel.Provider, sel.Model, root)
		fmt.Fprintln(out, "new session started")
		if autosave {
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, rt, sel, streaming, autosave, nil
	case "/provider":
		if arg == "" {
			fmt.Fprintf(out, "current provider: %s\n", valueOr(sel.Provider, "(none)"))
			fmt.Fprintf(out, "available providers: %s\n", strings.Join(config.ProviderNames(cfg), ", "))
			return false, rt, sel, streaming, autosave, nil
		}
		nextSel, err := config.ResolveSelection(cfg, mustLoadAuth(), arg, "", "")
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		p, err := requireProvider(nextSel)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		nextRT := buildRuntime(cfg, nextSel, p, root, reader, out, rt.Logger)
		fmt.Fprintf(out, "provider set to %s\n", nextSel.Provider)
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, nil
	case "/model":
		if arg == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			models, err := rt.Provider.ListModels(ctx)
			if err != nil {
				return false, rt, sel, streaming, autosave, err
			}
			for _, m := range models {
				marker := " "
				if m.ID == sel.Model {
					marker = "*"
				}
				fmt.Fprintf(out, "%s %s\n", marker, m.ID)
			}
			return false, rt, sel, streaming, autosave, nil
		}
		nextSel := sel
		nextSel.Model = arg
		p, err := requireProvider(nextSel)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		nextRT := buildRuntime(cfg, nextSel, p, root, reader, out, rt.Logger)
		fmt.Fprintf(out, "model set to %s\n", arg)
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, nil
	case "/stream":
		if arg == "" {
			fmt.Fprintf(out, "streaming: %s\n", onOff(streaming))
			return false, rt, sel, streaming, autosave, nil
		}
		switch strings.ToLower(arg) {
		case "on":
			fmt.Fprintln(out, "streaming: on")
			return false, rt, sel, true, autosave, nil
		case "off":
			fmt.Fprintln(out, "streaming: off")
			return false, rt, sel, false, autosave, nil
		default:
			return false, rt, sel, streaming, autosave, fmt.Errorf("usage: /stream [on|off]")
		}
	case "/autosave":
		if arg == "" {
			fmt.Fprintf(out, "autosave: %s\n", onOff(autosave))
			return false, rt, sel, streaming, autosave, nil
		}
		switch strings.ToLower(arg) {
		case "on":
			fmt.Fprintln(out, "autosave: on")
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
			return false, rt, sel, streaming, true, nil
		case "off":
			fmt.Fprintln(out, "autosave: off")
			return false, rt, sel, streaming, false, nil
		default:
			return false, rt, sel, streaming, autosave, fmt.Errorf("usage: /autosave [on|off]")
		}
	case "/sessions":
		infos, err := filteredSessions(config.SessionsDir(), arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if len(infos) == 0 {
			fmt.Fprintln(out, "no saved sessions")
			return false, rt, sel, streaming, autosave, nil
		}
		printSessions(out, infos)
		return false, rt, sel, streaming, autosave, nil
	case "/save":
		path, err := saveSessionNamed(rt, sel, arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		fmt.Fprintf(out, "saved: %s\n", path)
		return false, rt, sel, streaming, autosave, nil
	case "/load":
		path, err := resolveLoadTarget(reader, out, config.SessionsDir(), arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if path == "" {
			fmt.Fprintln(out, "load cancelled")
			return false, rt, sel, streaming, autosave, nil
		}
		loaded, err := session.Load(path)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		nextSel := sel
		if loaded.Provider != "" {
			nextSel.Provider = loaded.Provider
		}
		if loaded.Model != "" {
			nextSel.Model = loaded.Model
		}
		cfg2, nextSel2, err := loadSelection(commonOptions{provider: nextSel.Provider, model: nextSel.Model})
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		p, err := requireProvider(nextSel2)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		root := rt.Policy.Root
		if loaded.CWD != "" {
			if detected, detectErr := workspace.DetectRoot(loaded.CWD); detectErr == nil {
				root = detected
			}
		}
		nextRT := buildRuntime(cfg2, nextSel2, p, root, reader, out, rt.Logger)
		loaded.Provider = nextSel2.Provider
		loaded.Model = nextSel2.Model
		loaded.CWD = root
		nextRT.Session = loaded
		fmt.Fprintf(out, "loaded: %s\n", path)
		return false, nextRT, nextSel2, streaming, autosave, nil
	default:
		return false, rt, sel, streaming, autosave, fmt.Errorf("unknown command: %s", cmd)
	}
}

func resolveSessionPath(dir, arg string) string {
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, os.PathSeparator) {
		return arg
	}
	name := arg
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	return filepath.Join(dir, name)
}

func resolveLoadTarget(reader *bufio.Reader, out io.Writer, dir, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		infos, err := filteredSessions(dir, "")
		if err != nil {
			return "", err
		}
		if len(infos) == 0 {
			return "", nil
		}
		printSessions(out, infos)
		fmt.Fprint(out, "Load which session? [number/id, blank=cancel] ")
		choice, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		arg = strings.TrimSpace(choice)
		if arg == "" {
			return "", nil
		}
		return resolveSessionArg(dir, infos, arg)
	}
	infos, err := filteredSessions(dir, "")
	if err != nil {
		return "", err
	}
	return resolveSessionArg(dir, infos, arg)
}

func resolveSessionArg(dir string, infos []session.Info, arg string) (string, error) {
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, os.PathSeparator) {
		return arg, nil
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(infos) {
			return "", fmt.Errorf("session index out of range: %d", idx)
		}
		return infos[idx-1].Path, nil
	}
	for _, info := range infos {
		if info.ID == arg {
			return info.Path, nil
		}
	}
	path := resolveSessionPath(dir, arg)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	matches, err := filteredSessions(dir, arg)
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0].Path, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple sessions match %q; use /sessions %s and load by index", arg, arg)
	}
	return "", fmt.Errorf("session not found: %s", arg)
}

func parseSessionIndex(arg string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err == nil {
		return n, true
	}
	return 0, false
}

func filteredSessions(dir, filter string) ([]session.Info, error) {
	infos, err := session.List(dir)
	if err != nil {
		return nil, err
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return infos, nil
	}
	out := make([]session.Info, 0, len(infos))
	for _, info := range infos {
		hay := strings.ToLower(info.ID + " " + info.Provider + " " + info.Model + " " + filepath.Base(info.Path))
		if strings.Contains(hay, filter) {
			out = append(out, info)
		}
	}
	return out, nil
}

func printSessions(out io.Writer, infos []session.Info) {
	for i, info := range infos {
		fmt.Fprintf(out, "%2d. %s  %s  %s  %s\n", i+1, info.ID, info.UpdatedAt.Format("2006-01-02 15:04:05"), valueOr(info.Provider, "-"), valueOr(info.Model, "-"))
	}
}

func validateSessionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name must not be empty")
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf("session name must not contain path separators")
	}
	return nil
}

func mustLoadAuth() *config.Auth {
	auth, _ := config.LoadAuth()
	if auth == nil {
		return &config.Auth{}
	}
	return auth
}

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
		return s[:97] + "..."
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

func saveSession(rt *agent.Runtime, sel config.Selection) (string, error) {
	return saveSessionNamed(rt, sel, "")
}

func saveSessionNamed(rt *agent.Runtime, sel config.Selection, name string) (string, error) {
	if rt == nil || rt.Session == nil {
		return "", fmt.Errorf("no session to save")
	}
	target := rt.Session
	if name != "" {
		if err := validateSessionName(name); err != nil {
			return "", err
		}
		clone := *rt.Session
		if rt.Session.Messages != nil {
			clone.Messages = append([]session.Message(nil), rt.Session.Messages...)
		}
		clone.ID = name
		target = &clone
	}
	target.Provider = sel.Provider
	target.Model = sel.Model
	root, err := workspace.DetectRoot(rt.Policy.Root)
	if err == nil {
		target.CWD = root
	}
	return session.Save(config.SessionsDir(), target)
}

func exitChat(reader *bufio.Reader, out io.Writer, rt *agent.Runtime, sel config.Selection, autosave bool) error {
	if _, err := saveSession(rt, sel); err != nil {
		return err
	}
	if !autosave {
		fmt.Fprintln(out, "session saved on exit")
	}
	fmt.Fprint(out, "Save named snapshot before exit? [y/N] ")
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return nil
	}
	fmt.Fprint(out, "Snapshot name (blank = current id): ")
	name, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	name = strings.TrimSpace(name)
	path, err := saveSessionNamed(rt, sel, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "saved snapshot: %s\n", path)
	return nil
}
