# oi

Tiny agent runtime for local workflows.

`oi` is a small command-line agent built around simple protocols, safe local tools, and OpenAI-compatible providers. It is written in Go and intentionally avoids external runtime dependencies.

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

## Design goals

- small core
- protocol-first interfaces
- safe local tool execution
- simple install/uninstall
- no framework dependency
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

When attached to a TTY, `oi` uses a small shell-style line editor (`internal/lineedit`):

- editable prompt with left/right cursor movement, home/end, backspace, delete
- history prev/next
- bracketed paste support
- slash command picker: type `/` to see available commands, arrow keys to navigate, enter/tab to pick
- arrow-key pickers for `/model`, `/think`, `/stream`, `/tools`, `/autosave`, `/login`, `/session`
- plain streaming output in the terminal flow — no fullscreen takeover
- shell scrollback remains usable
- `/status` shows model, context usage, thinking level, and session info on demand
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

Sessions autosave after successful turns and save on exit by default. Use `/compact` to collapse the current session into a summary.

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
internal/lineedit shell-style line editor and picker
internal/chat     interactive chat loop and commands
internal/agent    agent loop and tool-call handling
internal/provider OpenAI-compatible providers
internal/tool     local tool registry
internal/workspace workspace policy/safety
internal/rpc      NDJSON stdio protocol
internal/session  session persistence/compaction
internal/config   JSON config/auth
internal/log      debug logs
```

## Project status

Usable, still evolving. Current focus is keeping the runtime small, safe, scriptable, and protocol-friendly rather than adding a large plugin system or heavy UI.
