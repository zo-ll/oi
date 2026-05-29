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
# same as: oi chat

oi doctor
oi models
oi providers
oi login openai-codex  # ChatGPT browser login, uses your subscription
oi login openai        # OpenAI Platform API key, separate from ChatGPT
oi logout openai-codex
oi version
oi run "task"
oi chat
oi rpc
```

## Current status

Available now:

```bash
oi
# default chat mode

oi doctor
oi models
oi providers
oi login openai-codex
oi login openai
oi logout openai-codex
oi version
oi run "task"
oi rpc
oi chat
```

`chat` is implemented as a minimal interactive mode with slash commands and streaming output.

## Chat slash commands

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

Chat autosaves the rolling session by default after successful turns, saves on exit, and can optionally save a named snapshot when exiting.

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

In chat, `/login` is a two-step flow:

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

Use `--debug` with `oi chat` or `oi run` to write JSONL debug logs under:

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
