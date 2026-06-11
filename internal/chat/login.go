package chat

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
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
	picker, hasPicker := out.(pickerUI)
	if kind == "" && providerName == "" {
		var err error
		if hasPicker {
			kind, err = pickerLoginKind(picker)
		} else {
			kind, err = promptLoginKind(reader, out)
		}
		if err != nil {
			return nil, sel, err
		}
		if kind == "" {
			return rt, sel, nil
		}
	}
	if kind != "" {
		if providerName == "" {
			choice, err := pickLoginProviderChoice(reader, out, cfg, kind, sel.Provider)
			if err != nil {
				return nil, sel, err
			}
			if choice == "" {
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

func pickLoginProviderChoice(reader *bufio.Reader, out io.Writer, cfg *config.Config, kind, current string) (string, error) {
	if picker, ok := out.(pickerUI); ok {
		return pickerLoginProviderChoice(picker, cfg, kind)
	}
	return promptLoginProviderChoice(reader, out, cfg, kind, current)
}

func promptLoginProviderChoice(reader *bufio.Reader, out io.Writer, cfg *config.Config, kind, current string) (string, error) {
	names := loginProviderNames(cfg, kind)
	if len(names) == 0 {
		fmt.Fprintln(out, "available providers: (none)")
		return "", nil
	}
	auth := mustLoadAuth()
	fmt.Fprintln(out, "Provider?")
	for i, name := range names {
		marker := " "
		if providerForDisplaySelection(kind, name) == current {
			marker = "*"
		}
		label := loginProviderLabel(auth, kind, name)
		fmt.Fprintf(out, "%2d. %s %s\n", i+1, marker, label)
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

func pickerLoginKind(picker pickerUI) (string, error) {
	selected, ok := picker.overlayPicker("choose login type", []string{
		"sub  ChatGPT subscription / browser login",
		"api  Provider API key",
	})
	if !ok || strings.TrimSpace(selected) == "" {
		return "", nil
	}
	fields := strings.Fields(selected)
	if len(fields) == 0 {
		return "", nil
	}
	return normalizeLoginKind(fields[0])
}

func pickerLoginProviderChoice(picker pickerUI, cfg *config.Config, kind string) (string, error) {
	names := loginProviderNames(cfg, kind)
	if len(names) == 0 {
		return "", nil
	}
	auth := mustLoadAuth()
	labels := make([]string, 0, len(names))
	labelToName := make(map[string]string, len(names))
	for _, name := range names {
		label := loginProviderLabel(auth, kind, name)
		labels = append(labels, label)
		labelToName[label] = name
	}
	selected, ok := picker.overlayPicker("choose provider", labels)
	if !ok || strings.TrimSpace(selected) == "" {
		return "", nil
	}
	if name := labelToName[selected]; name != "" {
		return name, nil
	}
	return canonicalProviderName(selected), nil
}

func loginProviderLabel(auth *config.Auth, kind, name string) string {
	label := canonicalProviderName(name)
	providerName := providerForDisplaySelection(kind, name)
	if strings.TrimSpace(auth.Keys[providerName]) != "" {
		return label + " [configured]"
	}
	if _, ok := auth.OAuth[providerName]; ok {
		return label + " [configured]"
	}
	return label
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
		for _, name := range []string{"openai", "openrouter", "groq", "deepseek", "together", "fireworks", "perplexity", "mistral", "xai", "cerebras", "sambanova", "opencode-go"} {
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

func providerDisplayName(name string) string {
	return canonicalProviderName(name)
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
