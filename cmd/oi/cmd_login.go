package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/oauth"
)

func runLogin(args []string, in io.Reader, w io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts struct {
		provider string
		apiKey   string
		baseURL  string
	}
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key to save")
	fs.StringVar(&opts.baseURL, "base-url", "", "provider base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.provider == "" && len(fs.Args()) > 0 {
		opts.provider = strings.TrimSpace(fs.Args()[0])
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return err
	}
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]config.ProviderConfig)
	}
	providerName := canonicalProviderName(opts.provider)
	if providerName == "" {
		providerName = cfg.SelectedProvider
	}
	if providerName == "" {
		return fmt.Errorf("usage: oi login <provider> [--api-key KEY] [--base-url URL]")
	}

	pc, ok := cfg.Providers[providerName]
	if !ok {
		if known, ok := knownProviderProfile(providerName); ok {
			pc = known
		}
	}
	if opts.baseURL != "" {
		pc.BaseURL = opts.baseURL
	}
	if pc.BaseURL == "" {
		return fmt.Errorf("provider %q is not configured; pass --base-url or add it to config.json", providerName)
	}
	cfg.Providers[providerName] = pc

	if providerName == "openai-codex" {
		return runLoginOpenAICodex(in, w, cfg, auth, providerName, pc, opts)
	}

	key := normalizeAPIKey(opts.apiKey)
	if key == "" {
		fmt.Fprintf(w, "Paste API key for %s (input will be visible): ", providerName)
		line, err := promptLine(in)
		if err != nil {
			return err
		}
		key = normalizeAPIKey(line)
	}
	if key == "" {
		return fmt.Errorf("API key is required")
	}
	if auth.Keys == nil {
		auth.Keys = make(map[string]string)
	}
	delete(auth.OAuth, providerName)
	auth.Keys[providerName] = key
	if err := config.Save(cfg); err != nil {
		return err
	}
	if err := config.SaveAuth(auth); err != nil {
		return err
	}

	fmt.Fprintf(w, "saved provider: %s\n", providerName)
	fmt.Fprintf(w, "config: %s\n", config.ConfigPath())
	fmt.Fprintf(w, "auth: %s\n", config.AuthPath())
	if providerName == "openai" {
		fmt.Fprintln(w, "note: OpenAI requires an API key from platform.openai.com. A ChatGPT web subscription does not provide API access.")
	}
	sel, err := config.ResolveSelection(cfg, auth, providerName, "", "")
	if err == nil {
		fmt.Fprintf(w, "connectivity: %s\n", doctorConnectivity(sel))
	}
	return nil
}

func runLoginOpenAICodex(in io.Reader, w io.Writer, cfg *config.Config, auth *config.Auth, providerName string, pc config.ProviderConfig, opts struct {
	provider string
	apiKey   string
	baseURL  string
}) error {
	if auth.OAuth == nil {
		auth.OAuth = make(map[string]oauth.OpenAICodexCredentials)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cred, err := oauth.LoginOpenAICodex(ctx, func(info oauth.AuthInfo) {
		fmt.Fprintf(w, "Open this URL to log in:\n%s\n", info.URL)
		if info.Instructions != "" {
			fmt.Fprintln(w, info.Instructions)
		}
		if err := openBrowser(info.URL); err != nil {
			fmt.Fprintf(w, "warning: could not open browser automatically: %v\n", err)
		}
	}, func(message string) (string, error) {
		fmt.Fprint(w, message)
		return promptLine(in)
	})
	if err != nil {
		return err
	}
	cfg.Providers[providerName] = pc
	delete(auth.Keys, providerName)
	auth.OAuth[providerName] = cred
	if err := config.Save(cfg); err != nil {
		return err
	}
	if err := config.SaveAuth(auth); err != nil {
		return err
	}
	fmt.Fprintf(w, "saved provider: %s\n", providerName)
	fmt.Fprintf(w, "config: %s\n", config.ConfigPath())
	fmt.Fprintf(w, "auth: %s\n", config.AuthPath())
	sel, err := config.ResolveSelection(cfg, auth, providerName, "", "")
	if err == nil {
		fmt.Fprintf(w, "connectivity: %s\n", doctorConnectivity(sel))
	}
	return nil
}

func runLogout(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var providerName string
	fs.StringVar(&providerName, "provider", "", "provider name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if providerName == "" && len(fs.Args()) > 0 {
		providerName = strings.TrimSpace(fs.Args()[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if providerName == "" {
		providerName = cfg.SelectedProvider
	}
	if providerName == "" {
		return fmt.Errorf("usage: oi logout <provider>")
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return err
	}
	delete(auth.Keys, providerName)
	delete(auth.OAuth, providerName)
	if err := config.SaveAuth(auth); err != nil {
		return err
	}
	fmt.Fprintf(w, "removed saved key for %s\n", providerName)
	if pc, ok := cfg.Providers[providerName]; ok && pc.APIKeyEnv != "" && strings.TrimSpace(os.Getenv(pc.APIKeyEnv)) != "" {
		fmt.Fprintf(w, "note: %s is still set in the environment and will continue to take precedence\n", pc.APIKeyEnv)
	}
	return nil
}

func normalizeAPIKey(v string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "Bearer "))
}

func promptLine(in io.Reader) (string, error) {
	reader, ok := in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(in)
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
