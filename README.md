# oi

Tiny agent runtime for local workflows.

`oi` is a small command-line agent built around simple protocols, safe local tools, and OpenAI-compatible providers. It is written in Go and has one first-party dependency, [`tide`](https://github.com/zo-ll/tide), for terminal primitives used by the interactive TUI. The core runtime packages are standard-library only.

## What it does

- Interactive terminal agent: `oi`
- One-shot tasks: `oi run "..."`
- NDJSON stdio RPC for bridges/bots/tools: `oi rpc`
- OpenAI-compatible provider support
- ChatGPT subscription login through the Codex backend
- OpenAI Platform API-key login
- OpenCode Go provider support
- Local tools with workspace policy
- Session save/autosave/compact support
- Debug JSONL logs

## Harness shape

`oi` is meant to be a **small embeddable agent harness** you can run directly on a VPS or drive from another process. The product has three frontends over the same core:

- `oi` — human-facing interactive chat/TUI
- `oi run` — one-shot non-interactive execution
- `oi rpc` — machine-facing NDJSON stdio protocol

The intended core underneath those frontends is:
- `internal/agent` — agent loop / streaming / tool-call handling
- `internal/provider` — provider abstraction + OpenAI-compatible backends
- `internal/tool` — tool registry + execution
- `internal/workspace` — policy / approvals / path safety
- `internal/session` — persistence + compaction
- `internal/rpc` — machine protocol surface

The TUI is optional app shell, not the core product. Terminal UI lives in `internal/chat` and [`tide`](https://github.com/zo-ll/tide); the harness stays useful without either.

## Design goals

- small core
- protocol-first interfaces
- safe local tool execution
- simple install/uninstall
- one first-party dependency (`tide`), stdlib-only core
- useful from terminal, scripts, and external bridges

## Install

Install to `~/.local/bin/oi`:

```bash
bash install.sh
```

Override install directory:

```bash
OI_INSTALL_DIR=/custom/bin bash install.sh
```

Uninstall:

```bash
bash uninstall.sh
```

Build manually:

```bash
go test ./...
go build -o ~/.local/bin/oi ./cmd/oi
```

Or install with Go:

```bash
go install github.com/zo-ll/oi/cmd/oi@latest
```

## Quick start

Check environment and auth state:

```bash
oi doctor
```

Login with ChatGPT subscription/Codex backend:

```bash
oi login openai-codex
```

Login with OpenAI Platform API key:

```bash
oi login openai
```

List models:

```bash
oi models
```

Start interactive mode:

```bash
oi
```

Run one task:

```bash
oi run "inspect this repository and summarize the architecture"
```

Start RPC mode:

```bash
oi rpc
```

## Commands

```bash
oi                      # interactive mode
oi --debug              # interactive mode with debug logs
oi doctor               # inspect config/auth/provider state
oi models               # list models for configured provider
oi login openai-codex   # ChatGPT browser login, uses subscription
oi login chatgpt        # alias for openai-codex
oi login openai         # OpenAI Platform API key
oi login opencode-go    # OpenCode Go provider
oi logout openai-codex
oi version
oi run "task"            # one-shot agent task
oi rpc                  # NDJSON stdio protocol
```

## Interactive mode

When attached to a TTY, `oi` runs a fullscreen TUI (alt screen, mouse-wheel scroll, overlay pickers) built on [`tide`](https://github.com/zo-ll/tide):

- editable prompt with cursor movement, home/end, backspace, multiline
- prompt history: up/down arrows recall previous inputs
- slash command hints: type `/` to browse commands, arrows to navigate, tab to fill
- arrow-key overlay pickers for `/model`, `/think`, `/stream`, `/tools`, `/autosave`, `/login`, `/session`
- streaming output in the transcript area; mouse wheel scrolls
- shift-drag selects text (terminal-native select, bypasses mouse capture)
- steering: type + enter while a turn is running to queue a follow-up; it is injected at the next safe boundary, not a hard abort
- approval modal: with `approval_mode: prompt`, mutating actions pause for a `y`/`n` overlay
- auto-compaction: session compacts at 90% of the context window by default before the next model call
- `/status` shows model, context usage, thinking level, auto-compact threshold, and session info
- line-mode fallback outside a TTY

Slash commands:

```text
/help
/status
/login
/model
/stream
/think
/tools
/autosave
/new
/save
/session
/compact
/clear
/exit
```

Sessions autosave after successful turns and save on exit by default. Use `/compact` to collapse the current session into a summary. Auto-compaction runs automatically at 90% of the context window; configure it with `agent.auto_compact_threshold` in `config.json` (set to `-1` to disable).

## Provider login

Use `oi doctor` to inspect configured providers and auth state.

In interactive mode, `/login` is a two-step flow:

1. Choose `sub` or `api`.
2. Choose a provider. For `sub`, the provider is `openai` through ChatGPT/Codex browser login.

`/login` only saves authentication. Use `/model` to select the actual model. `/model` persists both `selected_model` and the inferred `selected_provider`.

Notes:

- `oi login openai-codex` uses ChatGPT Plus/Pro browser OAuth against `https://chatgpt.com/backend-api`.
- `oi login openai` uses the standard OpenAI API endpoint and requires a separate Platform API key.
- `oi login opencode-go` uses OpenCode Go (`https://opencode.ai/zen/go/v1`).
- There is no separate `/provider` command. Provider switching is implicit through `/model`.

## RPC mode

`oi rpc` speaks newline-delimited JSON over stdio, useful for bridges and external tools.

Example:

```bash
printf '{"id":"1","type":"ping"}\n{"id":"2","type":"get_state"}\n' | oi rpc
```

See [RPC.md](RPC.md).

## Debug logging

Use `--debug` with `oi` or `oi run` to write JSONL debug logs under:

```text
~/.local/state/oi/logs/
```

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for package layout, runtime model, config, provider interface, RPC, tool policy, and non-goals.

High-level shape:

```text
cmd/oi            CLI commands
tide              terminal primitives (raw mode, alt screen, mouse, wrapping)
internal/chat     interactive TUI, chat loop, commands, pickers, login
internal/agent    agent loop and tool-call handling
internal/provider OpenAI-compatible providers
internal/oauth    ChatGPT/Codex OAuth login
internal/retrieval lightweight code retrieval for file questions
internal/tool     local tool registry
internal/workspace workspace policy/safety
internal/rpc      NDJSON stdio protocol
internal/session  session persistence/compaction
internal/config   JSON config/auth
internal/log      debug logs
```

## Project status

Usable, still evolving. Current focus is keeping the runtime small, safe, scriptable, protocol-friendly, and easier to embed headlessly rather than adding a large plugin system or heavier UI.
