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
- `/sessions`
- `/save [name]`
- `/load <name|path>`
- `/exit`

Chat autosaves the rolling session by default after successful turns, saves on exit, and can optionally save a named snapshot when exiting.

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
