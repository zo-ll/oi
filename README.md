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
oi login openai
oi logout openai
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
oi login openai
oi logout openai
oi version
oi run "task"
oi rpc
oi chat
```

`chat` is implemented as a minimal interactive mode with slash commands and streaming output.

## Chat slash commands

- `/help`
- `/provider [name]`
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

Save credentials for a provider:

```bash
oi login openai --model gpt-4.1
oi login opencode-go --model deepseek-v4-pro
```

`oi login openai` uses the standard OpenAI API endpoint (`https://api.openai.com/v1`).

Important: a ChatGPT web subscription does not automatically provide API access. For `openai`, you need an API key from `platform.openai.com`.

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
