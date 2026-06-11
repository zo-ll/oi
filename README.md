# oi

Tiny Go agent runtime.

## Status

Scaffolded rebuild in progress.

Current focus:
- OpenAI-compatible providers
- safe local tools
- stdio RPC for bot integration
- standard library only

## Install

```bash
cd ~/Projects/oi
bash install.sh
```

By default this installs `oi` to `~/.local/bin/oi`.
Override with:

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

## Commands

```bash
oi
# interactive mode

oi --debug
oi doctor
oi models
oi login openai-codex  # ChatGPT browser login, uses your subscription
oi login openai        # OpenAI Platform API key, separate from ChatGPT
oi logout openai-codex
oi version
oi run "task"
oi rpc
```

## Current status

Available now:

```bash
oi
# default interactive mode

oi doctor
oi models
oi login openai-codex
oi login openai
oi logout openai-codex
oi version
oi run "task"
oi rpc
```

Interactive mode uses a minimal stdlib-only terminal UI when attached to a TTY:
- wrapped output with cleaner plain-text rendering
- wrapped multiline input
- bracketed paste support
- `Ctrl+V` to paste from the system clipboard when available
- `Ctrl+Y` to copy the last assistant reply
- `Ctrl+K` to insert a newline
- dim one-line header with model context window when known
- per-turn context usage display when the provider reports token usage
- quieter tool/status output
- line-mode fallback when not attached to a terminal

## Interactive slash commands

- `/help`
- `/login` (choose `sub` or `api`, then provider)
- `/model`
- `/stream`
- `/tools`
- `/autosave`
- `/new`
- `/save`
- `/session`
- `/compact`
- `/clear`
- `/exit`

Interactive mode autosaves the rolling session by default after successful turns and saves on exit. `/save` asks for an optional name. Use `/compact` when you want to manually collapse the current session into a summary.

### Session examples

```text
/session
/save
/autosave
```

## Provider login

Use `oi doctor` to inspect configured providers and auth state.

In interactive mode, `/login` is a two-step flow:

1. Choose `sub` or `api`.
2. Choose a provider. For `sub`, the only provider is `openai` and it uses ChatGPT browser login.

`/login` only saves authentication. Use `/model` to make the actual selection. `/model` persists both `selected_model` and the inferred `selected_provider`.

There is no `/provider` command in interactive mode. Provider switching is implicit through `/model`.

Save credentials from the CLI:

```bash
oi login openai-codex  # ChatGPT browser login
oi login chatgpt       # alias for openai-codex
oi login openai        # OpenAI Platform API key
oi login opencode-go
```

- `oi login openai-codex` uses ChatGPT Plus/Pro browser OAuth against the Codex backend (`https://chatgpt.com/backend-api`). This is the ChatGPT subscription path, like browser-based login in coding tools.
- `oi` does not keep a default model. The current selection is stored as `selected_model`, and the provider is inferred from that model choice.
- `oi login openai` uses the standard OpenAI API endpoint (`https://api.openai.com/v1`) and requires a separate API key from `platform.openai.com`.
- `oi login opencode-go` uses OpenCode Go (`https://opencode.ai/zen/go/v1`). oi supports the current Go model families exposed through both `/chat/completions` and `/messages`.

If you have a ChatGPT Plus/Pro subscription and want browser login, use `openai-codex` / `chatgpt`, not `openai`.

## RPC example

```bash
printf '{"id":"1","type":"ping"}\n{"id":"2","type":"get_state"}\n' | oi rpc
```

See also: `RPC.md`

## Debug logging

Use `--debug` with `oi` or `oi run` to write JSONL debug logs under:

```bash
~/.local/state/oi/logs/
```

## Docs

- `ARCHITECTURE.md`
- `ROADMAP.md`

## Goals

- minimal
- highly functional
- well designed
- no external dependencies
