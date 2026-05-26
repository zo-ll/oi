# oi rebuild roadmap

This roadmap assumes a full rebuild in Go with the standard library only.

## Phase 0 — reset and freeze scope

### Goals
- keep the repo small
- define v1 boundaries before coding
- avoid importing `oi-old` structure directly

### Tasks
- keep current shell launcher as temporary legacy entrypoint
- treat `oi-old` as reference only
- lock v1 scope to: provider, tools, agent loop, rpc, sessions, logs
- defer TUI/RAG/vision/local backend

### Done when
- architecture doc exists
- roadmap exists
- package layout is agreed

---

## Phase 1 — project skeleton

### Goals
- create a clean codebase with strong boundaries

### Tasks
- initialize `cmd/oi`
- create packages:
  - `internal/config`
  - `internal/provider`
  - `internal/agent`
  - `internal/tool`
  - `internal/workspace`
  - `internal/rpc`
  - `internal/session`
  - `internal/log`
- define core types:
  - `Message`
  - `Request`
  - `Response`
  - `ToolCall`
  - `Session`
  - `Policy`
- add small unit tests for shared types/helpers

### Done when
- `go build ./...` succeeds
- packages compile with stub implementations
- no package knows too much about another

---

## Phase 2 — config and auth

### Goals
- make provider selection and credentials clean

### Tasks
- implement XDG path resolution
- implement `config.json` loader
- implement `auth.json` loader
- support env override precedence
- implement config validation
- add `oi doctor`

### Output
- `oi doctor` prints:
  - config path
  - auth path
  - selected provider
  - selected model
  - workspace root
  - provider connectivity result

### Done when
- config can be loaded without provider code knowing file details
- missing/invalid auth errors are clear

---

## Phase 3 — OpenAI-compatible provider

### Goals
- solid cloud backend first

### Tasks
- implement chat completions request
- implement non-streaming response parsing
- implement SSE streaming parser
- implement `list_models`
- normalize provider responses into:
  - final text
  - tool calls
  - usage
- support native tool calls
- add strict JSON fallback parsing for weak providers
- add request timeout and retry policy

### Tests
- parse normal response
- parse streaming content deltas
- parse streaming tool-call deltas
- parse provider error bodies

### Done when
- `oi run "hello"` can talk to an OpenAI-compatible endpoint
- streaming works reliably

---

## Phase 4 — workspace and policy

### Goals
- make local execution safe and predictable

### Tasks
- implement workspace root resolution
- implement path normalization and root checks
- implement symlink-safe path resolution
- implement shell policy checker
- implement approval modes:
  - `prompt`
  - `auto`
  - `never`
- implement truncation limits
- implement per-tool timeout handling

### Tests
- path escape attempts
- symlink escape attempts
- blocked shell commands
- write outside workspace

### Done when
- every mutating or risky action flows through one policy layer

---

## Phase 5 — tool system

### Goals
- small toolset, high leverage

### Tools for v1
- `read_file`
- `list_dir`
- `find_files`
- `grep`
- `run_command`
- `write_file`
- `replace_in_file`

### Tasks
- define JSON arg schema per tool
- implement tool registry
- implement structured results
- truncate outputs cleanly
- require approval where needed
- make tool errors visible to the model

### Notes
- `replace_in_file` should require a unique match by default
- `write_file` should create parent directories only inside allowed roots
- `run_command` should capture stdout+stderr together

### Done when
- tools are usable independently from the agent loop
- each tool has focused tests

---

## Phase 6 — agent loop

### Goals
- make the core agent runtime work end to end

### Tasks
- implement session history model
- implement system prompt
- implement loop:
  1. send history to provider
  2. receive final answer or tool calls
  3. execute tools
  4. append tool results
  5. repeat until final answer
- enforce max steps
- enforce total/step timeouts
- add plain-text final output path

### Tests
- final response with no tools
- one tool then final response
- multiple tool steps
- blocked tool then recovery
- max-steps exceeded

### Done when
- `oi run "inspect this repo"` works with real tool use

---

## Phase 7 — RPC mode

### Goals
- make `oi` controllable by your Telegram bot

### Tasks
- implement NDJSON stdin/stdout framing
- implement commands:
  - `ping`
  - `prompt`
  - `abort`
  - `new_session`
  - `get_state`
  - `set_provider`
  - `set_model`
  - `list_models`
  - `list_providers`
- implement events:
  - `ready`
  - `assistant_delta`
  - `assistant_done`
  - `tool_start`
  - `tool_result`
  - `error`
  - `done`
- wire cancellation through context

### Tests
- parse commands
- emit events in order
- abort while streaming
- abort while tool is running

### Done when
- a small bot bridge can keep one `oi rpc` process alive and drive it reliably

---

## Phase 8 — minimal interactive chat

### Goals
- a simple human CLI, not a large terminal app

### Tasks
- implement `oi chat`
- minimal line input
- streaming output
- small slash-command set:
  - `/help`
  - `/provider`
  - `/model`
  - `/new`
  - `/save`
  - `/load`
  - `/exit`
- no fancy TUI work

### Done when
- chat mode is pleasant enough without becoming a framework

---

## Phase 9 — sessions and logs

### Goals
- make runs inspectable and resumable

### Tasks
- persist session JSON files
- persist log JSONL files
- add request and tool timings
- add debug flag
- add session save/load helpers

### Done when
- failures can be diagnosed from logs
- conversations can be resumed

---

## Phase 10 — hardening and cleanup

### Goals
- make v1 solid

### Tasks
- review package boundaries
- reduce oversized files
- improve error messages
- add integration tests for key flows
- add `oi models`
- improve `oi doctor`
- document config and RPC protocol

### Done when
- v1 is understandable without reading the whole codebase
- core files stay small and focused

---

## Phase 11 — optional local backend

Only after cloud-first v1 is stable.

### Tasks
- add provider implementation for local `llama.cpp`
- reuse same provider interface
- reuse same agent loop and rpc
- keep local backend optional

### Rule
- local support must not distort the main architecture

---

## First build order I recommend

If starting coding now, do it in this exact order:

1. Phase 1 — skeleton
2. Phase 2 — config/auth
3. Phase 3 — provider
4. Phase 4 — workspace/policy
5. Phase 5 — tools
6. Phase 6 — agent loop
7. Phase 7 — rpc
8. Phase 8 — chat
9. Phase 9 — sessions/logs
10. Phase 10 — hardening
11. Phase 11 — optional local backend

---

## What to copy conceptually from `oi-old`

Keep the ideas:
- provider abstraction
- rpc mode
- sessions
- logging
- streaming

Do not copy the shape:
- giant `agent.go`
- XML-ish tool tags as the main protocol
- REPL-heavy feature sprawl
- mixed UI/provider/agent concerns

---

## Immediate next implementation step

Start with a compile-only skeleton and the following first test targets:
- config path resolution
- provider response normalization
- workspace path safety
- `replace_in_file`
- rpc framing

That gives the rebuild a clean spine before real features land.
