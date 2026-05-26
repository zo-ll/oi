package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
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
	rt := buildRuntime(cfg, sel, p, root, reader, out)

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
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, newRT, newSel, cmdErr := handleChatCommand(cfg, sel, rt, reader, out, line)
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
			} else {
				rt = newRT
				sel = newSel
			}
			if exit {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Agent.ToolTimeoutSeconds*cfg.Agent.MaxSteps+30)*time.Second)
		resp, runErr := rt.RunOnce(ctx, line)
		cancel()
		if runErr != nil {
			fmt.Fprintf(out, "error: %v\n", runErr)
		} else {
			fmt.Fprintln(out, resp)
		}
		if err == io.EOF {
			return nil
		}
	}
}

func handleChatCommand(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, line string) (bool, *agent.Runtime, config.Selection, error) {
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
		fmt.Fprintln(out, "/new                 start a new session")
		fmt.Fprintln(out, "/save [name]         save current session")
		fmt.Fprintln(out, "/load <name|path>    load a saved session")
		fmt.Fprintln(out, "/exit                exit chat")
		return false, rt, sel, nil
	case "/exit", "/quit":
		return true, rt, sel, nil
	case "/new":
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, err
		}
		rt.Session = session.New(sel.Provider, sel.Model, root)
		fmt.Fprintln(out, "new session started")
		return false, rt, sel, nil
	case "/provider":
		if arg == "" {
			fmt.Fprintf(out, "current provider: %s\n", valueOr(sel.Provider, "(none)"))
			fmt.Fprintf(out, "available providers: %s\n", strings.Join(config.ProviderNames(cfg), ", "))
			return false, rt, sel, nil
		}
		nextSel, err := config.ResolveSelection(cfg, mustLoadAuth(), arg, "", "")
		if err != nil {
			return false, rt, sel, err
		}
		p, err := requireProvider(nextSel)
		if err != nil {
			return false, rt, sel, err
		}
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, err
		}
		nextRT := buildRuntime(cfg, nextSel, p, root, reader, out)
		fmt.Fprintf(out, "provider set to %s\n", nextSel.Provider)
		return false, nextRT, nextSel, nil
	case "/model":
		if arg == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			models, err := rt.Provider.ListModels(ctx)
			if err != nil {
				return false, rt, sel, err
			}
			for _, m := range models {
				marker := " "
				if m.ID == sel.Model {
					marker = "*"
				}
				fmt.Fprintf(out, "%s %s\n", marker, m.ID)
			}
			return false, rt, sel, nil
		}
		nextSel := sel
		nextSel.Model = arg
		p, err := requireProvider(nextSel)
		if err != nil {
			return false, rt, sel, err
		}
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, err
		}
		nextRT := buildRuntime(cfg, nextSel, p, root, reader, out)
		fmt.Fprintf(out, "model set to %s\n", arg)
		return false, nextRT, nextSel, nil
	case "/save":
		if rt.Session == nil {
			return false, rt, sel, fmt.Errorf("no session to save")
		}
		if arg != "" {
			if err := validateSessionName(arg); err != nil {
				return false, rt, sel, err
			}
			rt.Session.ID = arg
		}
		rt.Session.Provider = sel.Provider
		rt.Session.Model = sel.Model
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err == nil {
			rt.Session.CWD = root
		}
		path, err := session.Save(config.SessionsDir(), rt.Session)
		if err != nil {
			return false, rt, sel, err
		}
		fmt.Fprintf(out, "saved: %s\n", path)
		return false, rt, sel, nil
	case "/load":
		if arg == "" {
			return false, rt, sel, fmt.Errorf("usage: /load <name|path>")
		}
		path := resolveSessionPath(config.SessionsDir(), arg)
		loaded, err := session.Load(path)
		if err != nil {
			return false, rt, sel, err
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
			return false, rt, sel, err
		}
		p, err := requireProvider(nextSel2)
		if err != nil {
			return false, rt, sel, err
		}
		root := rt.Policy.Root
		if loaded.CWD != "" {
			if detected, detectErr := workspace.DetectRoot(loaded.CWD); detectErr == nil {
				root = detected
			}
		}
		nextRT := buildRuntime(cfg2, nextSel2, p, root, reader, out)
		loaded.Provider = nextSel2.Provider
		loaded.Model = nextSel2.Model
		loaded.CWD = root
		nextRT.Session = loaded
		fmt.Fprintf(out, "loaded: %s\n", path)
		return false, nextRT, nextSel2, nil
	default:
		return false, rt, sel, fmt.Errorf("unknown command: %s", cmd)
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
