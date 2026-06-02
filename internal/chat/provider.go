package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/workspace"
)

func mustLoadAuth() *config.Auth {
	auth, _ := config.LoadAuth()
	if auth == nil {
		return &config.Auth{}
	}
	return auth
}

func loginAndSwitchChatProvider(deps Dependencies, cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, args []string) (*agent.Runtime, config.Selection, error) {
	kind, loginArgs := stripLoginKindArg(args)
	providerName := loginArgsProvider(loginArgs)
	if kind == "" && providerName == "" {
		var err error
		kind, err = promptLoginKind(reader, out)
		if err != nil {
			return nil, sel, err
		}
		if kind == "" {
			fmt.Fprintln(out, "login cancelled")
			return rt, sel, nil
		}
	}
	if kind != "" {
		if providerName == "" {
			choice, err := promptLoginProviderChoice(reader, out, cfg, kind, sel.Provider)
			if err != nil {
				return nil, sel, err
			}
			if choice == "" {
				fmt.Fprintln(out, "login cancelled")
				return rt, sel, nil
			}
			providerName = choice
		}
		var err error
		providerName, err = providerForLoginKind(kind, providerName)
		if err != nil {
			return nil, sel, err
		}
		loginArgs = withLoginProviderArg(loginArgs, providerName)
	} else if providerName == "" {
		return nil, sel, fmt.Errorf("provider is required")
	}
	if deps.Login == nil {
		return nil, sel, fmt.Errorf("login is unavailable")
	}
	if err := deps.Login(loginArgs, chatLoginReader(providerName, reader), out); err != nil {
		return nil, sel, err
	}
	fmt.Fprintln(out, "login saved; use /model")
	return rt, sel, nil
}

func switchChatProvider(deps Dependencies, cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, arg string) (*agent.Runtime, config.Selection, error) {
	target, err := resolveProviderChoiceName(cfg, arg)
	if err != nil {
		return nil, sel, err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cfg2, nextSel, err := reloadSelectionForChat(target, "")
		if err == nil {
			var pErr error
			p, pErr := requireProvider(nextSel)
			if pErr == nil {
				root, err := workspace.DetectRoot(rt.Policy.Root)
				if err != nil {
					return nil, sel, err
				}
				*cfg = *cfg2
				nextRT := buildRuntime(cfg, nextSel, p, root, reader, out, rt.Logger)
				fmt.Fprintf(out, "provider set to %s\n", nextSel.Provider)
				fmt.Fprintf(out, "model: %s\n", valueOr(nextSel.Model, "(none)"))
				return nextRT, nextSel, nil
			}
			err = pErr
		}
		lastErr = err
		if attempt > 0 {
			break
		}
		fmt.Fprintf(out, "provider %s is not ready: %v\n", target, err)
		yes, err := promptYesNo(reader, out, "Log in now? [y/N] ")
		if err != nil {
			return nil, sel, err
		}
		if !yes {
			break
		}
		if deps.Login == nil {
			return nil, sel, fmt.Errorf("login is unavailable")
		}
		if err := deps.Login([]string{target}, reader, out); err != nil {
			return nil, sel, err
		}
	}
	return nil, sel, lastErr
}

type readyModelChoice struct {
	Provider string
	Model    provider.Model
}

func switchChatModel(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, model string) (*agent.Runtime, config.Selection, error) {
	choice, err := resolveReadyModelChoice(model, sel.Provider)
	if err != nil {
		return nil, sel, err
	}
	return switchChatModelToChoice(cfg, sel, rt, reader, out, choice)
}

func switchChatModelToChoice(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, choice readyModelChoice) (*agent.Runtime, config.Selection, error) {
	nextSel := config.Selection{Provider: choice.Provider, Model: choice.Model.ID}
	cfg2, nextSel, err := loadSelection(commonOptions{provider: nextSel.Provider, model: nextSel.Model})
	if err != nil {
		return nil, sel, err
	}
	cfg2.SelectedProvider = choice.Provider
	cfg2.SelectedModel = choice.Model.ID
	if err := config.Save(cfg2); err != nil {
		return nil, sel, err
	}
	p, err := requireProvider(nextSel)
	if err != nil {
		return nil, sel, err
	}
	root, err := workspace.DetectRoot(rt.Policy.Root)
	if err != nil {
		return nil, sel, err
	}
	*cfg = *cfg2
	nextRT := buildRuntime(cfg, nextSel, p, root, reader, out, rt.Logger)
	fmt.Fprintf(out, "model set to %s [%s]\n", choice.Model.ID, choice.Provider)
	return nextRT, nextSel, nil
}

func promptModelChoice(reader *bufio.Reader, out io.Writer, current config.Selection) (string, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return "", err
	}
	return promptReadyModelChoice(reader, out, choices, current, "Switch model? [number/name, blank=keep] ")
}

func resolveReadyModelChoice(arg string, currentProvider string) (readyModelChoice, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return readyModelChoice{}, err
	}
	if len(choices) == 0 {
		return readyModelChoice{}, fmt.Errorf("no ready models; use /login")
	}
	return resolveReadyModelChoiceFromList(choices, arg, currentProvider)
}

func resolveReadyModelChoiceFromList(choices []readyModelChoice, arg string, currentProvider string) (readyModelChoice, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return readyModelChoice{}, fmt.Errorf("model is required")
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(choices) {
			return readyModelChoice{}, fmt.Errorf("model index out of range: %d", idx)
		}
		return choices[idx-1], nil
	}
	var matches []readyModelChoice
	for _, choice := range choices {
		if choice.Model.ID == arg || strings.EqualFold(choice.Model.ID, arg) {
			matches = append(matches, choice)
		}
	}
	if len(matches) == 0 {
		return readyModelChoice{}, fmt.Errorf("ready model not found: %s", arg)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	for _, choice := range matches {
		if choice.Provider == currentProvider {
			return choice, nil
		}
	}
	return readyModelChoice{}, fmt.Errorf("model %q is available from multiple providers; choose by index", arg)
}

func ensureReadyModelAfterLogin(reader *bufio.Reader, out io.Writer, cfg *config.Config, sel config.Selection) (*config.Config, config.Selection, provider.Provider, error) {
	choices, err := listReadyModelChoicesForProvider(sel.Provider)
	if err != nil || len(choices) == 0 || selectionHasReadyModel(sel, choices) {
		p, pErr := requireProvider(sel)
		return cfg, sel, p, pErr
	}
	choice, err := promptReadyModelChoice(reader, out, choices, sel, "Choose model? [number/name, blank=keep] ")
	if err != nil {
		return nil, sel, nil, err
	}
	if choice == "" {
		p, pErr := requireProvider(sel)
		return cfg, sel, p, pErr
	}
	nextCfg, nextSel, err := loadSelection(commonOptions{provider: sel.Provider, model: choice})
	if err != nil {
		return nil, sel, nil, err
	}
	p, err := requireProvider(nextSel)
	if err != nil {
		return nil, sel, nil, err
	}
	return nextCfg, nextSel, p, nil
}

func selectionHasReadyModel(sel config.Selection, choices []readyModelChoice) bool {
	if strings.TrimSpace(sel.Model) == "" {
		return false
	}
	for _, choice := range choices {
		if choice.Provider == sel.Provider && choice.Model.ID == sel.Model {
			return true
		}
	}
	return false
}

func listReadyModelChoicesForProvider(providerName string) ([]readyModelChoice, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(providerName) == "" {
		return choices, nil
	}
	var out []readyModelChoice
	for _, choice := range choices {
		if choice.Provider == providerName {
			out = append(out, choice)
		}
	}
	return out, nil
}

func promptReadyModelChoice(reader *bufio.Reader, out io.Writer, choices []readyModelChoice, current config.Selection, prompt string) (string, error) {
	if len(choices) == 0 {
		fmt.Fprintln(out, "no ready models; use /login")
		return "", nil
	}
	fmt.Fprintf(out, "current model: %s\n", valueOr(current.Model, "(none)"))
	printReadyModels(out, choices, current)
	fmt.Fprint(out, prompt)
	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	if _, err := resolveReadyModelChoiceFromList(choices, choice, current.Provider); err != nil {
		return "", err
	}
	return choice, nil
}

func listReadyModelChoices() ([]readyModelChoice, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, err
	}
	var out []readyModelChoice
	for _, name := range config.ProviderNames(cfg) {
		sel, err := config.ResolveSelection(cfg, auth, name, "", "")
		if err != nil {
			continue
		}
		p, err := requireProvider(sel)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		models, err := p.ListModels(ctx)
		cancel()
		if err != nil {
			continue
		}
		for _, model := range models {
			out = append(out, readyModelChoice{Provider: name, Model: model})
		}
	}
	return out, nil
}

func printReadyModels(out io.Writer, choices []readyModelChoice, current config.Selection) {
	singleProvider := true
	if len(choices) > 1 {
		providerName := choices[0].Provider
		for _, choice := range choices[1:] {
			if choice.Provider != providerName {
				singleProvider = false
				break
			}
		}
	}
	for i, choice := range choices {
		marker := " "
		if choice.Provider == current.Provider && choice.Model.ID == current.Model {
			marker = "*"
		}
		label := choice.Model.ID
		if strings.TrimSpace(choice.Model.Name) != "" && choice.Model.Name != choice.Model.ID {
			label += "  " + choice.Model.Name
		}
		if singleProvider {
			fmt.Fprintf(out, "%2d. %s %s\n", i+1, marker, label)
			continue
		}
		fmt.Fprintf(out, "%2d. %s %s  [%s]\n", i+1, marker, label, choice.Provider)
	}
}

func reloadSelectionForChat(providerName, modelName string) (*config.Config, config.Selection, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, config.Selection{}, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, config.Selection{}, err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, config.Selection{}, err
	}
	sel, err := config.ResolveSelection(cfg, auth, providerName, modelName, "")
	if err != nil {
		return nil, config.Selection{}, err
	}
	return cfg, sel, nil
}

func promptProviderChoice(reader *bufio.Reader, out io.Writer, cfg *config.Config, current string) (string, error) {
	names := providerChoiceNames(cfg)
	fmt.Fprintf(out, "current provider: %s\n", valueOr(current, "(none)"))
	if len(names) == 0 {
		fmt.Fprintln(out, "available providers: (none)")
		return "", nil
	}
	auth := mustLoadAuth()
	for i, name := range names {
		marker := " "
		if name == current {
			marker = "*"
		}
		note := authSource(name, cfg.Providers[name], auth)
		if _, ok := cfg.Providers[name]; !ok {
			note = "not configured"
		}
		fmt.Fprintf(out, "%2d. %s %s  %s\n", i+1, marker, providerDisplayName(name), note)
	}
	fmt.Fprint(out, "Switch provider? [number/name, blank=keep] ")
	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	return resolveProviderChoiceFromNames(names, choice)
}

func promptLoginKind(reader *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Login type?")
	fmt.Fprintln(out, " 1. sub  ChatGPT subscription / browser login")
	fmt.Fprintln(out, " 2. api  Provider API key")
	fmt.Fprint(out, "Login type? [sub/api, blank=cancel] ")
	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	return normalizeLoginKind(choice)
}

func promptLoginProviderChoice(reader *bufio.Reader, out io.Writer, cfg *config.Config, kind, current string) (string, error) {
	names := loginProviderNames(cfg, kind)
	if len(names) == 0 {
		fmt.Fprintln(out, "available providers: (none)")
		return "", nil
	}
	fmt.Fprintln(out, "Provider?")
	for i, name := range names {
		marker := " "
		if providerForDisplaySelection(kind, name) == current {
			marker = "*"
		}
		note := "configured"
		if _, ok := cfg.Providers[providerForDisplaySelection(kind, name)]; !ok {
			note = "known profile"
		}
		fmt.Fprintf(out, "%2d. %s %s  %s\n", i+1, marker, loginProviderDisplayName(kind, name), note)
	}
	fmt.Fprint(out, "Provider? [number/name, blank=cancel] ")
	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	return resolveProviderChoiceFromNames(names, choice)
}

func resolveProviderChoiceName(cfg *config.Config, arg string) (string, error) {
	return resolveProviderChoiceFromNames(providerChoiceNames(cfg), strings.TrimSpace(arg))
}

func resolveProviderChoiceFromNames(names []string, arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("provider name is required")
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(names) {
			return "", fmt.Errorf("provider index out of range: %d", idx)
		}
		return names[idx-1], nil
	}
	return canonicalProviderName(arg), nil
}

func providerChoiceNames(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var names []string
	add := func(name string) {
		name = canonicalProviderName(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range []string{"openai-codex", "opencode-go", "openai"} {
		add(name)
	}
	for _, name := range config.ProviderNames(cfg) {
		add(name)
	}
	return names
}

func providerDisplayName(name string) string {
	switch canonicalProviderName(name) {
	case "openai-codex":
		return "openai-codex / ChatGPT browser login (uses your subscription)"
	case "openai":
		return "openai / OpenAI API key (platform billing, not ChatGPT subscription)"
	case "opencode-go":
		return "opencode-go / OpenCode API key"
	default:
		return name
	}
}

func normalizeLoginKind(choice string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1", "sub", "subscription", "subscriber":
		return "sub", nil
	case "2", "api", "key", "apikey", "api-key":
		return "api", nil
	default:
		return "", fmt.Errorf("usage: choose sub or api")
	}
}

func stripLoginKindArg(args []string) (string, []string) {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		switch {
		case arg == "--provider" || arg == "--model" || arg == "--api-key" || arg == "--base-url":
			i++
			continue
		case strings.HasPrefix(arg, "-"):
			continue
		}
		kind, err := normalizeLoginKind(arg)
		if err != nil {
			return "", out
		}
		return kind, append(out[:i], out[i+1:]...)
	}
	return "", out
}

func loginProviderNames(cfg *config.Config, kind string) []string {
	switch kind {
	case "sub":
		return []string{"openai"}
	case "api":
		var out []string
		seen := make(map[string]bool)
		add := func(name string) {
			name = canonicalProviderName(name)
			if name == "" || name == "openai-codex" || seen[name] {
				return
			}
			seen[name] = true
			out = append(out, name)
		}
		for _, name := range []string{"openai", "opencode-go"} {
			add(name)
		}
		for _, name := range config.ProviderNames(cfg) {
			add(name)
		}
		return out
	default:
		return nil
	}
}

func providerForLoginKind(kind, providerName string) (string, error) {
	providerName = canonicalProviderName(providerName)
	switch kind {
	case "sub":
		switch providerName {
		case "openai", "openai-codex":
			return "openai-codex", nil
		default:
			return "", fmt.Errorf("subscription login currently supports only openai")
		}
	case "api":
		if providerName == "openai-codex" {
			return "", fmt.Errorf("api login does not use ChatGPT subscription; choose sub for openai browser login")
		}
		return providerName, nil
	default:
		return "", fmt.Errorf("usage: choose sub or api")
	}
}

func providerForDisplaySelection(kind, name string) string {
	providerName, err := providerForLoginKind(kind, name)
	if err != nil {
		return canonicalProviderName(name)
	}
	return providerName
}

func loginProviderDisplayName(kind, name string) string {
	if kind == "sub" && name == "openai" {
		return "openai / ChatGPT subscription browser login"
	}
	return providerDisplayName(name)
}

func withLoginProviderArg(args []string, providerName string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := strings.TrimSpace(out[i])
		switch {
		case arg == "--provider":
			if i+1 < len(out) {
				out[i+1] = providerName
				return out
			}
			return append(out, providerName)
		case strings.HasPrefix(arg, "--provider="):
			out[i] = "--provider=" + providerName
			return out
		case arg == "--model" || arg == "--api-key" || arg == "--base-url":
			i++
			continue
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			out[i] = providerName
			return out
		}
	}
	return append(out, providerName)
}

func loginArgsProvider(args []string) string {
	var providerName string
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--provider" && i+1 < len(args):
			providerName = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--provider="):
			providerName = strings.TrimSpace(strings.TrimPrefix(arg, "--provider="))
		case arg == "--api-key" || arg == "--base-url":
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(arg, "--api-key=") || strings.HasPrefix(arg, "--base-url="):
			// Not selection fields for the active chat after login.
		case !strings.HasPrefix(arg, "-") && providerName == "":
			providerName = arg
		}
	}
	return canonicalProviderName(providerName)
}

func chatLoginReader(providerName string, reader *bufio.Reader) io.Reader {
	if canonicalProviderName(providerName) == "openai-codex" {
		return strings.NewReader("")
	}
	return reader
}

func promptYesNo(reader *bufio.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
