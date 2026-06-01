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
oi --provider openai-codex --model gpt-5.3-codex
oi doctor
oi models
oi providers
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
oi providers
oi login openai-codex
oi login openai
oi logout openai-codex
oi version
oi run "task"
oi rpc
```

Interactive mode uses a minimal stdlib-only terminal UI when attached to a TTY:
- wrapped output
- wrapped multiline input
- bracketed paste support
- `Ctrl+V` to paste from the system clipboard when available
- `Ctrl+Y` to copy the last assistant reply
- `Ctrl+K` to insert a newline
- line-mode fallback when not attached to a terminal

## Interactive slash commands

- `/help`
- `/login` (choose `sub` or `api`, then provider)
- `/provider [name]` (blank opens a picker)
- `/model [name]`
- `/stream [on|off]`
- `/autosave [on|off]`
- `/new`
- `/sessions [filter]`
- `/save [name]`
- `/load [name|path|index]`
- `/exit`

Interactive mode autosaves the rolling session by default after successful turns, saves on exit, and can optionally save a named snapshot when exiting.

### Session examples

```text
/sessions
/sessions deepseek
/load
/load 1
/save refactor-snapshot
/autosave off
```

## Provider login

List configured providers:

```bash
oi providers
```

In interactive mode, `/login` is a two-step flow:

1. Choose `sub` or `api`.
2. Choose a provider. For `sub`, the only provider is `openai` and it uses ChatGPT browser login.

Save credentials from the CLI:

```bash
oi login openai-codex --model gpt-5.3-codex  # ChatGPT browser login
oi login chatgpt --model gpt-5.3-codex       # alias for openai-codex
oi login openai --model gpt-4.1              # OpenAI Platform API key
oi login opencode-go --model deepseek-v4-pro
```

- `oi login openai-codex` uses ChatGPT Plus/Pro browser OAuth against the Codex backend (`https://chatgpt.com/backend-api`). This is the ChatGPT subscription path, like browser-based login in coding tools.
- `oi login openai` uses the standard OpenAI API endpoint (`https://api.openai.com/v1`) and requires a separate API key from `platform.openai.com`.

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
