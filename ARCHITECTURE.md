# oi v1 architecture

## Goal

`oi` is a tiny agent runtime, implemented in Go, with:
- OpenAI-compatible providers
- local tool execution
- stdio RPC for Telegram/bot integration
- one first-party dependency (`tide`) for terminal primitives; stdlib-only core

It should be minimal in code size, but strong in architecture, safety, and functionality. Go is an implementation choice here: the product goal is a small protocol-first agent runtime, not a Go-only ecosystem.

> Note: the runtime originally targeted stdlib-only. It now depends on one small first-party package, [`tide`](https://github.com/zo-ll/tide), for terminal primitives (raw mode, alt screen, mouse, wrapping). The core packages (`agent`, `provider`, `tool`, `workspace`, `session`, `rpc`, `config`) remain stdlib-only. `tide` is the only dependency and isvendored via a `replace` directive in `go.mod`.

---

## Product shape

`oi` v1 should focus on 3 modes:

```bash
oi
oi run "find where auth is handled"
oi rpc
```

Supporting utility commands:

```bash
oi models
oi doctor
```

### Modes

- `oi`: interactive terminal mode
- `run`: one-shot agent task
- `rpc`: persistent NDJSON stdio protocol
- `models`: list models for the current provider
- `doctor`: validate config, auth, provider connectivity, workspace access

---

## Design principles

1. Cloud-first
   - v1 is built around OpenAI-compatible APIs
   - local backends are optional later

2. Small core, strong boundaries
   - config, provider, tools, agent loop, rpc are separate packages

3. Strict agent runtime
   - bounded steps
   - explicit tool results
   - deterministic transcript
   - no hidden side effects

4. Safe by default
   - workspace sandbox
   - approval for writes and shell commands
   - explicit `--unsafe` to relax policy

5. Minimal dependencies
   - core packages (`agent`, `provider`, `tool`, `workspace`, `session`, `rpc`, `config`) are stdlib-only
   - one first-party dependency: `tide` for terminal primitives used by the interactive TUI
   - no YAML/TOML, no third-party readline, no RPC framework

6. Protocol first
   - RPC mode is a first-class interface, not an afterthought

---

## Non-goals for v1

Do not build these first:
- RAG
- vision/image support
- heavyweight clipboard abstractions
- plugin system
- background autonomous daemons
- multi-agent orchestration
- local llama.cpp backend unless core cloud agent is already solid

> The interactive mode shipped a fullscreen TUI (alt screen, mouse wheel scroll, overlays) built on `tide`. This was an intentional product pivot from the original "plain terminal flow" design.

---

## Repo shape

```text
cmd/oi/
internal/config/
internal/provider/
internal/oauth/
internal/retrieval/
internal/agent/
internal/chat/
internal/tool/
internal/workspace/
internal/rpc/
internal/session/
internal/log/
```

### Package responsibilities

#### `internal/config`
- load config JSON
- load auth JSON
- resolve XDG paths
- resolve provider/model selection
- migrate legacy default keys on load only

#### `internal/provider`
- provider interface
- OpenAI-compatible implementation
- OpenCode Go provider with chat completions and messages backends
- SSE streaming parser
- tool-call normalization

#### `internal/agent`
- system prompt
- message history
- step loop
- tool-call handling
- final answer handling

#### `internal/chat`
- interactive chat loop (fullscreen TUI on a TTY, line-mode fallback otherwise)
- slash command handling
- streaming output, tool activity display, approval prompts
- model/thinking configuration, session and model pickers, login flow
- prompt history, steering queue, auto-compaction wiring

> The interactive TUI is implemented in `internal/chat/tui*.go` and uses `tide` for terminal primitives. The package is split by concern:
> - `tui.go` — app struct, main loop, turn execution, runtime wiring
> - `tui_render.go` — frame render, transcript, input line wrap
> - `tui_input.go` — byte reader, prompt history, escape/mouse, scroll, tab complete
> - `tui_overlay.go` — picker/approval/input modal overlays
>
> There is no separate `lineedit` package; the line editor lives in `tide` and the TUI overlays live in `chat`.

#### `internal/tool`
- tool registry
- argument validation
- tool execution
- result formatting

#### `internal/workspace`
- root detection
- safe path resolution
- path allow/deny rules
- shell command policy

#### `internal/rpc`
- NDJSON request/response/event protocol
- request dispatch
- cancellation
- per-session state

#### `internal/session`
- transcript persistence
- summaries
- session metadata

#### `internal/log`
- structured logs
- request/tool timing
- debug traces

---

## Runtime model

A single `Runtime` owns:
- current provider
- current model
- current workspace root
- session history
- tool registry
- policy

Suggested internal shape:

```go
type Runtime struct {
    Provider provider.Provider
    Tools    *tool.Registry
    Policy   workspace.Policy
    Session  *session.Session
    Config   *config.Config
}
```

---

## Config model

Use JSON only.

### Config path

- config: `$XDG_CONFIG_HOME/oi/config.json`
- auth: `$XDG_CONFIG_HOME/oi/auth.json`
- state: `$XDG_STATE_HOME/oi/`

Fallbacks:
- `~/.config/oi/`
- `~/.local/state/oi/`

### `config.json`

```json
{
  "selected_provider": "openrouter",
  "selected_model": "openai/gpt-4.1-mini",
  "providers": {
    "openrouter": {
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_env": "OPENROUTER_API_KEY"
    },
    "deepseek": {
      "base_url": "https://api.deepseek.com/v1",
      "api_key_env": "DEEPSEEK_API_KEY"
    }
  },
  "agent": {
    "max_tool_output_bytes": 65536,
    "tool_timeout_seconds": 20,
    "request_timeout_seconds": 600,
    "approval_mode": "auto",
    "auto_compact_threshold": 90
  }
}
```

### `auth.json`

Optional, `0600` only:

```json
{
  "keys": {
    "openrouter": "...",
    "deepseek": "..."
  }
}
```

Resolution order:
1. CLI flags
2. env vars
3. auth.json
4. config selection (`selected_provider` / `selected_model`)

### `agent` options

- `max_tool_output_bytes`: truncate tool output beyond this (default 65536)
- `tool_timeout_seconds`: per-tool timeout (default 20)
- `request_timeout_seconds`: per-provider-request timeout (default 600)
- `approval_mode`: `auto` | `prompt` | `never` (default `auto`)
- `auto_compact_threshold`: auto-compact the session before a model call when usage reaches this percent of the context window. `0` means the default (90%), a negative value disables auto-compaction. Compaction target is 70% of the window.

---

## Provider model

v1 needs one provider family: OpenAI-compatible chat completions.

### Provider interface

```go
type Provider interface {
    Name() string
    Model() string
    SetModel(string)
    Chat(context.Context, Request) (Response, error)
    ChatStream(context.Context, Request) (<-chan Event, error)
    ListModels(context.Context) ([]Model, error)
}
```

### Request model

Internal request format should be provider-neutral.

```go
type Message struct {
    Role    string
    Content string
}

type Request struct {
    Model    string
    Messages []Message
    Tools    []ToolSpec
    Stream   bool
}
```

### Response normalization

Normalize all provider outputs into one shape:

```go
type Response struct {
    Content   string
    ToolCalls []ToolCall
    Usage     Usage
}

type ToolCall struct {
    ID   string
    Name string
    Args json.RawMessage
}
```

### Tool calling strategy

Primary:
- use native OpenAI tool calls when the provider supports them reliably

Fallback:
- strict JSON output from the model

Do not use XML-like tool tags in the new design.

---

## Tool system

Keep the tool surface small and high leverage.

### v1 tools

1. `read_file`
2. `list_dir`
3. `find_files`
4. `grep`
5. `run_command`
6. `write_file`
7. `replace_in_file`

### Why `replace_in_file` is core

For an agent, full-file writes are too blunt. Exact replacement is safer, smaller, and more inspectable.

### Tool interface

```go
type Tool interface {
    Name() string
    Spec() ToolSpec
    Run(context.Context, Call) Result
}
```

### Rules

- tools receive structured JSON args
- tools return structured results
- results are truncated and marked if needed
- writes and commands respect approval policy

---

## Workspace and safety model

v1 should have a strict workspace model.

### Workspace root

Default root:
- current working directory

Optional overrides:
- `--cwd`
- configured allowed roots

### Path rules

- all relative paths resolve against workspace root
- deny escaping root unless explicitly allowed
- deny writes outside allowed roots
- resolve symlinks before access checks

### Shell rules

`run_command` is allowed, but constrained.

#### v1 approach

- execute through `sh -c` or `bash -c`
- apply policy before execution
- block known dangerous patterns
- require approval by default

Blocked examples:
- `rm -rf /`
- disk/partition ops
- fork bombs
- privilege escalation
- background daemons

### Approval modes

- `auto`: no prompt inside workspace safe policy (default)
- `prompt`: ask before writes/commands
- `never`: read-only, deny mutations

---

## Agent loop

The loop should be simple and explicit.

### Flow

1. drain any queued steering messages into history (before the next model call)
2. auto-compact history if usage exceeds the configured threshold
3. build request from session history
4. send to provider
5. if provider returns final content, finish (unless steering was drained, then continue)
6. if provider returns tool calls, validate them
7. execute tools one by one
8. append tool results to history
9. drain steering messages again (after tool results, before next model call)
10. repeat until final answer or limit reached

### Limits

- internal agent step cap
- max tool output bytes
- per-tool timeout
- per-provider-request timeout
- auto-compaction at a configurable context-window threshold (default 90%, target 70%)

### Failure behavior

- invalid tool args -> tool error result back to model
- blocked command -> tool error result back to model
- provider timeout -> stop and return error
- internal agent step cap exceeded -> stop with explicit failure

### History model

Keep history explicit:
- system
- user
- assistant
- tool

No hidden planner state in v1.

---

## RPC mode

RPC should use newline-delimited JSON over stdin/stdout.

This is simpler than full JSON-RPC 2.0 and easier for a Telegram bridge.

### Commands

- `ping`
- `prompt`
- `abort`
- `new_session`
- `get_state`
- `set_provider`
- `set_model`
- `list_models`
- `list_providers`

### Events

- `ready`
- `assistant_delta`
- `assistant_done`
- `tool_start`
- `tool_result`
- `error`
- `done`

### Example

Request:

```json
{"id":"1","type":"prompt","message":"summarize this repo"}
```

Events:

```json
{"type":"assistant_delta","id":"1","delta":"I’ll inspect the repo."}
{"type":"tool_start","id":"1","tool":"list_dir","args":{"path":"."}}
{"type":"tool_result","id":"1","tool":"list_dir","ok":true,"output":"..."}
{"type":"assistant_done","id":"1","message":"This repo contains ..."}
{"type":"done","id":"1"}
```

### RPC runtime rules

- one active task at a time per process in v1
- `abort` cancels provider call and pending tools
- all outputs are machine-readable
- no ANSI in RPC mode

---

## CLI behavior

### `oi`
- interactive terminal loop
- fullscreen TUI when attached to a TTY (alt screen, mouse-wheel scroll, overlays), built on `tide`
- plain line-mode fallback outside a TTY
- streaming by default
- `/status` shows model, context, thinking, session, and auto-compact info on demand
- commands: `/help`, `/status`, `/login`, `/model`, `/stream`, `/think`, `/tools`, `/autosave`, `/new`, `/save`, `/session`, `/compact`, `/clear`, `/exit`
- arrow-key overlay pickers for model, thinking level, session, and other choices
- slash command hints: type `/` to browse commands, arrows to navigate, tab to fill
- prompt history: up/down arrows recall previous inputs
- steering: typing + enter while a turn is running queues a follow-up; it is injected at the next safe boundary (after current turn/tool results, before the next model call) rather than hard-aborting
- mouse wheel scrolls the transcript; shift-drag (terminal-native) selects text
- approval modal: when `approval_mode` is `prompt`, mutating actions pause and show a `y`/`n` overlay
- auto-compaction: by default the session compacts at 90% of the context window before the next model call
- provider switching is implicit via model selection
- `/login` only stores auth; `/model` performs selection
- `/compact` manually collapses the current session into a summary

### `oi run`
- one-shot request
- prints final answer
- optional `--json`

### `oi rpc`
- long-lived process for Telegram bridge

### `oi models`
- list models for the currently selected provider

### `oi doctor`
- show config path
- show resolved provider/model
- verify API key exists
- verify provider connectivity
- verify workspace root
- show selected provider/model when set

---

## Sessions and state

Persist transcripts and metadata.

### Storage

- session files in `$XDG_STATE_HOME/oi/sessions/`
- log files in `$XDG_STATE_HOME/oi/logs/`

### Session format

Use JSONL or JSON.

Recommendation:
- one JSON file per session for simplicity
- optional text export later

Suggested shape:

```json
{
  "id": "20260526-123000-abc123",
  "provider": "openrouter",
  "model": "openai/gpt-4.1-mini",
  "cwd": "/path/to/project",
  "messages": []
}
```

---

## Logging

Logs should be structured and boring.

Minimum log records:
- startup
- config resolution
- provider request timing
- tool execution timing
- blocked actions
- fatal errors

Prefer JSONL for debug logs.

---

## Testing priorities

Because stdlib-only code is manageable, test the important edges heavily.

### Must-test units

- config resolution
- provider SSE parsing
- provider tool-call parsing
- path sandbox checks
- command blocking policy
- `replace_in_file`
- agent loop max-step behavior
- RPC request/response framing

### Must-test flows

- provider returns plain text final answer
- provider returns tool call then final answer
- blocked tool call is surfaced correctly
- abort during stream

---

## v1 success criteria

`oi` v1 is successful if it:
- works with your current OpenAI-compatible subscriptions
- can inspect and edit repo files safely
- can be driven reliably by a Telegram bot over stdio RPC
- remains dependency-free and understandable
- has a clean enough core that local backends can be added later without redesign

---

## v2 candidates

Only after v1 is stable:
- optional local `llama.cpp` provider
- patch/diff tool
- project summaries
- RAG
- vision
