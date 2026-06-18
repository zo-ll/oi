package chat

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	ilog "github.com/zo-ll/oi/internal/log"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/retrieval"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type Dependencies struct {
	Login func(args []string, in io.Reader, out io.Writer) error
}

type commonOptions struct {
	provider string
	model    string
	apiKey   string
	debug    bool
	rest     []string
}

type approvalPrompter interface {
	Approve(action, target string) (bool, error)
}

func buildRuntime(cfg *config.Config, sel config.Selection, p provider.Provider, root string, in io.Reader, out io.Writer, logger *ilog.Logger) *agent.Runtime {
	policy := workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalMode(cfg.Agent.ApprovalMode)}
	toolOpts := tool.Options{
		Policy:         policy,
		MaxOutputBytes: cfg.Agent.MaxToolOutputBytes,
		PromptInput:    in,
		PromptOutput:   out,
	}
	if prompter, ok := out.(approvalPrompter); ok {
		toolOpts.Approve = prompter.Approve
	}
	tools := tool.NewBuiltinRegistry(toolOpts)
	model := sel.Model
	if p != nil && p.Model() != "" {
		model = p.Model()
	}
	info := lookupModelInfo(p, model)
	level := clampThinkingLevel(info, "off")
	return &agent.Runtime{
		Provider:                p,
		Tools:                   tools,
		Policy:                  policy,
		Session:                 session.New(sel.Provider, model, root),
		ToolTimeout:             time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second,
		RequestTimeout:          time.Duration(cfg.Agent.RequestTimeoutSeconds) * time.Second,
		ContextWindow:           info.ContextWindow,
		ThinkingLevel:           level,
		ThinkingValue:           thinkingValue(info, level),
		ThinkingFormat:          info.ThinkingFormat,
		ThinkingSupported:       info.SupportsThinking,
		SupportedThinkingLevels: supportedThinkingLevels(info),
		ThinkingLevelValues:     info.ThinkingLevelValues,
		AutoCompactThreshold:    cfg.Agent.AutoCompactThreshold,
		Logger:                  logger,
	}
}

func maybeDebugLogger(mode string, enabled bool) (*ilog.Logger, error) {
	if !enabled {
		return nil, nil
	}
	path := filepath.Join(config.LogsDir(), fmt.Sprintf("%s-%s.jsonl", mode, time.Now().UTC().Format("20060102-150405")))
	return ilog.NewJSONL(path)
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

func requireProvider(sel config.Selection) (provider.Provider, error) {
	return provider.NewForSelection(sel)
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
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

func canonicalProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt", "codex", "openai-browser", "openai-chatgpt":
		return "openai-codex"
	default:
		return strings.TrimSpace(name)
	}
}

func interactiveProvider(sel config.Selection) (provider.Provider, string, error) {
	if sel.Provider == "" {
		return nil, "No provider configured. Use /login to set one up.", nil
	}
	p, err := requireProvider(sel)
	if err != nil {
		return nil, fmt.Sprintf("Provider %s is not ready: %v. Use /login, then /model.", sel.Provider, err), nil
	}
	if strings.TrimSpace(sel.Model) == "" {
		return p, "No model selected. Use /model.", nil
	}
	if notice := selectedModelStartupNotice(p, sel.Model); notice != "" {
		return p, notice, nil
	}
	return p, "", nil
}

func selectedModelStartupNotice(p provider.Provider, model string) string {
	if p == nil || strings.TrimSpace(model) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil || len(models) == 0 {
		return ""
	}
	for _, item := range models {
		if item.ID == model {
			return ""
		}
	}
	return fmt.Sprintf("Selected model %s is unavailable. Use /model.", model)
}

// Output formatting and text cleaning. One concern: style helpers, the
// streaming output formatter, display-text normalization, and status-line
// fragments. No tool-specific or UI-wiring logic.

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

func formatStatusContextUsage(window int, usage provider.Usage) string {
	if window <= 0 {
		return ""
	}
	if usage.InputTokens <= 0 {
		return "0 / " + formatCount(window) + " (0%)"
	}
	pct := usage.InputTokens * 100 / window
	return fmt.Sprintf("%s / %s (%d%%)", formatCount(usage.InputTokens), formatCount(window), pct)
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

var displayReplacer = strings.NewReplacer(
	"**", "",
	"__", "",
	"`", "",
	"\u00a0", " ",
	"â€™", "’",
	"â€œ", "“",
	"â€\x9d", "”",
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
	text = stripTerminalEscapes(text)
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

func stripTerminalEscapes(text string) string {
	out := make([]rune, 0, len(text))
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != 0x1b {
			out = append(out, runes[i])
			continue
		}
		if i+1 >= len(runes) {
			break
		}
		i++
		switch runes[i] {
		case '[':
			for i+1 < len(runes) {
				i++
				if runes[i] >= 0x40 && runes[i] <= 0x7e {
					break
				}
			}
		case ']':
			for i+1 < len(runes) {
				i++
				if runes[i] == '\a' {
					break
				}
				if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '\\' {
					i++
					break
				}
			}
		default:
			// Drop one-character escape sequences as well.
		}
	}
	return string(out)
}
