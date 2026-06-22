# RPC protocol

`oi rpc` speaks newline-delimited JSON over stdin/stdout.

It is the machine-facing embedding surface for **long-lived callers**. For
one-shot machine use, prefer:

- `oi run --json "task"`
- `oi run --ndjson "task"`

## Transport

- one JSON object per line
- UTF-8 only
- stdout carries protocol events
- stderr is reserved for process-level failures before the server starts
- no ANSI / terminal formatting in RPC mode

## Session model

`oi rpc` is now **multi-session**.

One server process can host multiple independent in-memory sessions at once.
Each session has its own:

- provider
- model
- runtime
- transcript/session state
- busy/abort state

There is also an **active session**. Requests that omit `session_id` target the
active session.

## Concurrency model

- prompts are **single-flight per session**
- different sessions can run prompts concurrently
- `abort` cancels one target session, not the whole server
- provider/model/session mutations are rejected only for the target session if
  that session is busy

## Requests

Each request is one JSON object with:

- `type` — required
- `id` — optional request correlation id
- `session_id` — optional target session id for session-scoped commands

Shape:

```json
{"id":"1","type":"ping"}
```

Supported request types:

## Global / session-management requests

### `ping`
Health check.

```json
{"id":"1","type":"ping"}
```

### `get_state`
Get state for the target session (or active session if omitted), plus the full
session list and current `active_session_id`.

```json
{"id":"1","type":"get_state"}
{"id":"1","type":"get_state","session_id":"repo-a"}
```

### `list_sessions`
List all in-memory sessions.

```json
{"id":"1","type":"list_sessions"}
```

### `create_session`
Create a new in-memory session.

```json
{"id":"1","type":"create_session"}
```

With explicit id / provider / model:

```json
{"id":"1","type":"create_session","session_id":"repo-a","provider":"openai","model":"gpt-4.1-mini"}
```

Rules:
- if `session_id` is omitted, oi generates one
- if omitted, provider/model inherit from the active session/template
- emits a `session` event with the new session state

### `use_session`
Make a session the active default target.

```json
{"id":"1","type":"use_session","session_id":"repo-a"}
```

### `close_session`
Close one in-memory session.

```json
{"id":"1","type":"close_session","session_id":"repo-a"}
```

Rules:
- rejected if the target session is busy
- if the closed session was active, another session becomes active
- if the last session is closed, oi creates a fresh replacement session

## Existing requests

### `list_providers`
List configured providers from `config.json`.

```json
{"id":"1","type":"list_providers"}
```

### `list_models`
List models for the target session's current provider.

```json
{"id":"1","type":"list_models","session_id":"repo-a"}
```

### `set_provider`
Switch the provider for one session.

```json
{"id":"1","type":"set_provider","session_id":"repo-a","provider":"openai"}
```

Rules:
- rejected if that session is busy
- resets that session runtime/transcript state

### `set_model`
Switch the model for one session.

```json
{"id":"1","type":"set_model","session_id":"repo-a","model":"gpt-4.1-mini"}
```

Rules:
- rejected if that session is busy
- preserves the session transcript

### `new_session`
Reset the target session transcript in place.

```json
{"id":"1","type":"new_session","session_id":"repo-a"}
```

This is different from `create_session`:
- `create_session` adds a new live session
- `new_session` clears one existing session's transcript

### `abort`
Abort the active prompt for the target session.

```json
{"id":"1","type":"abort","session_id":"repo-a"}
```

### `prompt`
Run one agent turn in the target session.

```json
{"id":"1","type":"prompt","session_id":"repo-a","message":"summarize this repository"}
```

Rules:
- rejected if that session already has a running prompt
- other sessions may still run concurrently
- emits `started`
- emits streaming `assistant_delta` events as visible answer text arrives
- emits `tool_start` / `tool_result`
- emits `assistant_done`
- emits `done` as the terminal event for that `(id, session_id)` pair
- on failure emits `error` then `done`

## Events

All session-scoped events include `session_id`.

Common event types:

- `ready`
- `pong`
- `state`
- `sessions`
- `session`
- `providers`
- `models`
- `started`
- `tool_start`
- `tool_result`
- `assistant_delta`
- `assistant_done`
- `done`
- `error`
- `aborted`

### `ready`
Emitted once when the server starts.

```json
{
  "type": "ready",
  "data": {
    "active_session_id": "20260622-120000",
    "session_id": "20260622-120000",
    "provider": "openai",
    "model": "gpt-4.1-mini",
    "workspace": "/repo",
    "busy": false,
    "message_count": 0,
    "sessions": [
      {
        "session_id": "20260622-120000",
        "provider": "openai",
        "model": "gpt-4.1-mini",
        "workspace": "/repo",
        "busy": false,
        "is_active": true,
        "message_count": 0
      }
    ]
  }
}
```

### `state`
Current state view for one target session plus the full session list.

### `sessions`
Session list snapshot.

### `session`
Returned after `create_session` or `new_session`.

### `tool_start`
Tool call started.

```json
{
  "type": "tool_start",
  "id": "req-1",
  "session_id": "repo-a",
  "data": {
    "name": "read_file",
    "args": {"path": "README.md"}
  }
}
```

### `tool_result`
Tool call finished.

```json
{
  "type": "tool_result",
  "id": "req-1",
  "session_id": "repo-a",
  "data": {
    "name": "read_file",
    "result": {
      "tool": "read_file",
      "ok": true,
      "output": "...",
      "meta": {"path": "README.md"}
    }
  }
}
```

### `assistant_delta`
Visible assistant output chunk.

```json
{"type":"assistant_delta","id":"req-1","session_id":"repo-a","delta":"hello"}
```

Notes:
- visible answer deltas only
- provider-native reasoning/thinking deltas are not emitted today
- callers should treat this as incremental text, not semantic sentence boundaries

### `assistant_done`
Final full assistant answer.

```json
{"type":"assistant_done","id":"req-1","session_id":"repo-a","message":"hello world"}
```

### `error`
Request-level error.

```json
{"type":"error","id":"req-1","session_id":"repo-a","error":"no provider configured"}
```

### `aborted`
Response to `abort`.

```json
{"type":"aborted","id":"req-2","session_id":"repo-a","data":{"had_active_request":true}}
```

### `done`
Terminal event for one request in one session.

```json
{"type":"done","id":"req-1","session_id":"repo-a"}
```

A caller should treat `(id, session_id)` as the completion boundary.

## Event ordering for `prompt`

Successful prompt shape:

1. `started`
2. zero or more `tool_start` / `tool_result`
3. zero or more `assistant_delta`
4. `assistant_done`
5. `done`

Failed prompt shape:

1. `started`
2. optional tool / delta events before failure
3. `error`
4. `done`

Aborted prompt shape:

- the `abort` request gets its own `aborted` event
- the target prompt still terminates through its normal request stream,
  usually `error` then `done`

## Approval / policy semantics

RPC mode is **headless**. There is no interactive approval round-trip on the
RPC wire today.

That means:
- read-only actions allowed by policy proceed normally
- if policy allows a mutating action automatically, it proceeds
- if policy requires interactive approval and no approval callback exists, the
  tool fails and that failure is surfaced through `tool_result` / `error`

RPC does **not** bypass workspace policy.

## Example

```bash
printf '{"id":"1","type":"create_session","session_id":"repo-a"}\n{"id":"2","type":"create_session","session_id":"repo-b"}\n{"id":"3","type":"prompt","session_id":"repo-a","message":"say hi"}\n{"id":"4","type":"prompt","session_id":"repo-b","message":"list models"}\n' | oi rpc
```

Events from different sessions may interleave. Clients should route by
`session_id` (and usually also `id`).

## Stability notes

The protocol is intentionally small. The current contract is stable enough for:
- bots/bridges
- supervisors
- shell-driven integrations
- personal VPS harness use

If new event types are added later, existing event meanings should remain
compatible.
