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
	iprovider "github.com/zo-ll/oi/internal/provider"
	irpc "github.com/zo-ll/oi/internal/rpc"
	"github.com/zo-ll/oi/internal/workspace"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "oi:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runChat(nil, stdin, stdout)
	}

	switch args[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	case "version", "--version", "-v":
		printVersion(stdout)
		return nil
	case "doctor":
		return runDoctor(args[1:], stdout)
	case "models":
		return runModels(args[1:], stdout)
	case "providers":
		return runProviders(stdout)
	case "login":
		return runLogin(args[1:], stdin, stdout)
	case "logout":
		return runLogout(args[1:], stdout)
	case "run":
		return runTask(args[1:], stdin, stdout)
	case "rpc":
		return runRPC(stdin, stdout)
	case "chat":
		return runChat(args[1:], stdin, stdout)
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "oi")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  oi                  # start chat")
	fmt.Fprintln(w, "  oi help")
	fmt.Fprintln(w, "  oi doctor")
	fmt.Fprintln(w, "  oi models")
	fmt.Fprintln(w, "  oi providers")
	fmt.Fprintln(w, "  oi login [provider]")
	fmt.Fprintln(w, "  oi logout [provider]")
	fmt.Fprintln(w, "  oi version")
	fmt.Fprintln(w, "  oi chat")
	fmt.Fprintln(w, "  oi run \"task\"")
	fmt.Fprintln(w, "  oi rpc")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Current status: chat is the default mode; doctor, models, providers, login, logout, version, run, and rpc are available.")
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oi %s\n", version)
	fmt.Fprintf(w, "commit: %s\n", commit)
	fmt.Fprintf(w, "built: %s\n", date)
	fmt.Fprintf(w, "go: %s\n", runtime.Version())
}

func runDoctor(args []string, w io.Writer) error {
	opts, err := parseCommonOptions("doctor", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return err
	}
	sel, err := config.ResolveSelection(cfg, auth, opts.provider, opts.model, opts.apiKey)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot(cwd)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "config: %s\n", config.ConfigPath())
	fmt.Fprintf(w, "auth: %s\n", config.AuthPath())
	fmt.Fprintf(w, "state: %s\n", config.StateDir())
	fmt.Fprintf(w, "workspace: %s\n", root)

	providers := config.ProviderNames(cfg)
	if len(providers) == 0 {
		fmt.Fprintln(w, "providers: (none configured)")
	} else {
		fmt.Fprintf(w, "providers: %s\n", strings.Join(providers, ", "))
	}

	if sel.Provider == "" {
		fmt.Fprintln(w, "selected provider: (none)")
	} else {
		fmt.Fprintf(w, "selected provider: %s\n", sel.Provider)
		fmt.Fprintf(w, "selected model: %s\n", valueOr(sel.Model, "(none)"))
		fmt.Fprintf(w, "base url: %s\n", valueOr(sel.BaseURL, "(none)"))
		fmt.Fprintf(w, "api key: %s\n", yesNo(sel.APIKey != ""))
		fmt.Fprintf(w, "connectivity: %s\n", doctorConnectivity(sel))
	}

	fmt.Fprintf(w, "agent max steps: %d\n", cfg.Agent.MaxSteps)
	fmt.Fprintf(w, "tool timeout seconds: %d\n", cfg.Agent.ToolTimeoutSeconds)
	fmt.Fprintf(w, "request timeout seconds: %d\n", cfg.Agent.RequestTimeoutSeconds)
	fmt.Fprintf(w, "approval mode: %s\n", cfg.Agent.ApprovalMode)
	return nil
}

func runModels(args []string, w io.Writer) error {
	opts, err := parseCommonOptions("models", args)
	if err != nil {
		return err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	_ = cfg
	p, err := requireProvider(sel)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		marker := " "
		if m.ID == sel.Model {
			marker = "*"
		}
		fmt.Fprintf(w, "%s %s\n", marker, m.ID)
	}
	return nil
}

func runProviders(w io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return err
	}
	names := config.ProviderNames(cfg)
	if len(names) == 0 {
		fmt.Fprintln(w, "no providers configured")
		return nil
	}
	for _, name := range names {
		pc := cfg.Providers[name]
		marker := " "
		if name == cfg.DefaultProvider {
			marker = "*"
		}
		fmt.Fprintf(w, "%s %s\n", marker, name)
		fmt.Fprintf(w, "  base_url: %s\n", pc.BaseURL)
		if pc.APIKeyEnv != "" {
			fmt.Fprintf(w, "  api_key_env: %s\n", pc.APIKeyEnv)
		}
		fmt.Fprintf(w, "  key_source: %s\n", authSource(name, pc, auth))
	}
	return nil
}

func runLogin(args []string, in io.Reader, w io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts struct {
		provider    string
		apiKey      string
		baseURL     string
		model       string
		makeDefault bool
	}
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key to save")
	fs.StringVar(&opts.baseURL, "base-url", "", "provider base URL")
	fs.StringVar(&opts.model, "model", "", "default model")
	fs.BoolVar(&opts.makeDefault, "default", false, "set as default provider")
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
	providerName := strings.TrimSpace(opts.provider)
	if providerName == "" {
		providerName = cfg.DefaultProvider
	}
	if providerName == "" {
		return fmt.Errorf("usage: oi login <provider> [--api-key KEY] [--base-url URL] [--model MODEL]")
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
	if cfg.DefaultProvider == "" || opts.makeDefault {
		cfg.DefaultProvider = providerName
	}
	if opts.model != "" && (cfg.DefaultModel == "" || opts.makeDefault || cfg.DefaultProvider == providerName) {
		cfg.DefaultModel = opts.model
	}

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
	sel, err := config.ResolveSelection(cfg, auth, providerName, firstNonEmpty(opts.model, cfg.DefaultModel), "")
	if err == nil {
		fmt.Fprintf(w, "connectivity: %s\n", doctorConnectivity(sel))
	}
	return nil
}

func runLoginOpenAICodex(in io.Reader, w io.Writer, cfg *config.Config, auth *config.Auth, providerName string, pc config.ProviderConfig, opts struct {
	provider    string
	apiKey      string
	baseURL     string
	model       string
	makeDefault bool
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
	if cfg.DefaultProvider == "" || opts.makeDefault {
		cfg.DefaultProvider = providerName
	}
	if cfg.DefaultModel == "" || opts.makeDefault || cfg.DefaultProvider == providerName {
		cfg.DefaultModel = firstNonEmpty(opts.model, "gpt-5.3-codex")
	}
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
	sel, err := config.ResolveSelection(cfg, auth, providerName, firstNonEmpty(opts.model, cfg.DefaultModel), "")
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
		providerName = cfg.DefaultProvider
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
	if pc, ok := cfg.Providers[providerName]; ok && pc.APIKeyEnv != "" && os.Getenv(pc.APIKeyEnv) != "" {
		fmt.Fprintf(w, "note: %s is still set in the environment and will continue to take precedence\n", pc.APIKeyEnv)
	}
	return nil
}

func runTask(args []string, stdin io.Reader, w io.Writer) error {
	opts, err := parseCommonOptions("run", args)
	if err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(opts.rest, " "))
	if prompt == "" {
		return fmt.Errorf("usage: oi run [--provider NAME] [--model NAME] [--api-key KEY] \"task\"")
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, err := requireProvider(sel)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot(cwd)
	if err != nil {
		return err
	}
	logger, err := maybeDebugLogger("run", opts.debug)
	if err != nil {
		return err
	}
	runtime := buildRuntime(cfg, sel, p, root, stdin, w, logger)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Agent.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := runtime.RunOnce(ctx, prompt)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, out)
	return nil
}

func runRPC(in io.Reader, w io.Writer) error {
	srv, err := irpc.NewServer()
	if err != nil {
		return err
	}
	return srv.Serve(in, w)
}

func doctorConnectivity(sel config.Selection) string {
	if sel.Provider == "" {
		return "skipped (no provider selected)"
	}
	if sel.APIKey == "" {
		return "skipped (no API key)"
	}
	p, err := iprovider.NewForSelection(sel)
	if err != nil {
		return "error: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("ok (%d models)", len(models))
}

func parseCommonOptions(name string, args []string) (commonOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts commonOptions
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.model, "model", "", "model name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key override")
	fs.BoolVar(&opts.debug, "debug", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return commonOptions{}, err
	}
	opts.rest = fs.Args()
	return opts, nil
}

func loadSelection(opts commonOptions) (*config.Config, config.Selection, error) {
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
	sel, err := config.ResolveSelection(cfg, auth, opts.provider, opts.model, opts.apiKey)
	if err != nil {
		return nil, config.Selection{}, err
	}
	return cfg, sel, nil
}

func requireProvider(sel config.Selection) (iprovider.Provider, error) {
	return iprovider.NewForSelection(sel)
}

type commonOptions struct {
	provider string
	model    string
	apiKey   string
	debug    bool
	rest     []string
}

func knownProviderProfile(name string) (config.ProviderConfig, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		return config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}, true
	case "openai-codex":
		return config.ProviderConfig{BaseURL: "https://chatgpt.com/backend-api"}, true
	case "opencode-go":
		return config.ProviderConfig{BaseURL: "https://opencode.ai/zen/go/v1", APIKeyEnv: "OPENCODE_API_KEY"}, true
	default:
		return config.ProviderConfig{}, false
	}
}

func authSource(name string, pc config.ProviderConfig, auth *config.Auth) string {
	if pc.APIKeyEnv != "" && strings.TrimSpace(os.Getenv(pc.APIKeyEnv)) != "" {
		return "env:" + pc.APIKeyEnv
	}
	if auth != nil {
		if strings.TrimSpace(auth.Keys[name]) != "" {
			return "auth.json"
		}
		if _, ok := auth.OAuth[name]; ok {
			return "oauth"
		}
	}
	return "none"
}

func normalizeAPIKey(v string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "Bearer "))
}

func promptLine(in io.Reader) (string, error) {
	line, err := bufio.NewReader(in).ReadString('\n')
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
