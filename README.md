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
oi doctor
oi models
oi version
```

`chat`, `run`, and `rpc` are scaffolded but not implemented yet.

## Docs

- `ARCHITECTURE.md`
- `ROADMAP.md`

## Goals

- minimal
- highly functional
- well designed
- no external dependencies
