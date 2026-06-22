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

## Concurrency model

- exactly **one active `prompt`** at a time per process
- `prompt` is single-flight
- `abort` cancels the active request if one is running
- provider/model/session mutations are rejected while a prompt is running

## Requests

Each request is one JSON object with:

- `type` — required
- `id` — optional correlation id echoed back on response/events for that request

Shape:

```json
{"id":"1","type":"ping"}
```

Supported request types:

### `ping`
Health check.

```json
{"id":"1","type":"ping"}
```

Emits:

```json
{"type":"pong","id":"1"}
```

### `get_state`
Return current provider/model/workspace/session state.

```json
{"id":"1","type":"get_state"}
```

Emits a `state` event.

### `list_providers`
List configured providers from `config.json`.

```json
{"id":"1","type":"list_providers"}
```

Emits a `providers` event.

### `list_models`
List models for the current provider.

```json
{"id":"1","type":"list_models"}
```

Emits a `models` event.

### `set_provider`
Switch the active provider and reset the in-memory session.

```json
{"id":"1","type":"set_provider","provider":"openai"}
```

Rules:
- rejected while a prompt is running
- resets the current in-memory session state
- emits a `state` event on success

### `set_model`
Switch the active model.

```json
{"id":"1","type":"set_model","model":"gpt-4.1-mini"}
```

Rules:
- rejected while a prompt is running
- updates provider/session model state in memory
- emits a `state` event on success

### `new_session`
Reset the current in-memory session.

```json
{"id":"1","type":"new_session"}
```

Rules:
- rejected while a prompt is running
- emits a `session` event with the new state

### `abort`
Cancel the active prompt if one exists.

```json
{"id":"1","type":"abort"}
```

Always emits an `aborted` event with:

```json
{"type":"aborted","id":"1","data":{"had_active_request":true}}
```

### `prompt`
Run one agent turn.

```json
{"id":"1","type":"prompt","message":"summarize this repository"}
```

Rules:
- rejected if another prompt is already running
- emits `started` immediately on acceptance
- emits streaming `assistant_delta` events as visible answer text arrives
- emits `tool_start` / `tool_result` around tool execution
- emits `assistant_done` with the final full answer
- emits `done` as the terminal event for that request
- on failure emits `error` then `done`

## Events

Common event types:

### `ready`
Emitted once when the server starts.

```json
{"type":"ready","data":{"provider":"openai","model":"gpt-4.1-mini","workspace":"/repo","busy":false,"session_id":"...","message_count":0}}
```

### `pong`
Response to `ping`.

### `state`
Current provider/model/workspace/session state.

Data shape:

```json
{
  "provider": "openai",
  "model": "gpt-4.1-mini",
  "workspace": "/repo",
  "busy": false,
  "session_id": "20260622-...",
  "message_count": 4
}
```

### `providers`
Configured provider names.

### `models`
Model list for the active provider. Current selection is marked with `* `.

### `session`
New session state after `new_session`.

### `started`
Accepted `prompt` has started.

### `tool_start`
A tool call started.

Data shape:

```json
{
  "name": "read_file",
  "args": {"path":"README.md"}
}
```

### `tool_result`
A tool call finished.

Data shape:

```json
{
  "name": "read_file",
  "result": {
    "tool": "read_file",
    "ok": true,
    "output": "...",
    "meta": {"path":"README.md"}
  }
}
```

### `assistant_delta`
Visible assistant output chunk.

```json
{"type":"assistant_delta","id":"1","delta":"hello"}
```

Notes:
- these are **visible answer deltas** only
- provider-native reasoning/thinking deltas are not emitted on the RPC wire today
- do not assume sentence or token boundaries beyond “more text arrived”

### `assistant_done`
Final full assistant answer.

```json
{"type":"assistant_done","id":"1","message":"hello world"}
```

### `error`
Request-level error.

```json
{"type":"error","id":"1","error":"no provider configured"}
```

### `aborted`
Response to `abort`.

### `done`
Terminal event for a request.

A caller should treat `done` as the completion boundary for a specific `id`.

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

- `abort` request gets its own `aborted` event
- the active prompt still terminates through its normal request stream, usually `error` then `done`

## Approval / policy semantics

RPC mode is **headless**. There is no interactive approval round-trip on the RPC wire today.

That means:
- read-only actions allowed by policy proceed normally
- if policy allows a mutating action automatically, it proceeds
- if policy requires interactive approval and no approval callback exists, the tool fails and that failure is surfaced through `tool_result` / `error`

In other words: RPC does **not** bypass workspace policy, but it also does not currently provide a separate approval-response protocol.

## Example

```bash
printf '{"id":"1","type":"ping"}\n{"id":"2","type":"get_state"}\n{"id":"3","type":"prompt","message":"say hi"}\n' | oi rpc
```

Example event flow:

```json
{"type":"ready","data":{"provider":"openai","model":"gpt-4.1-mini","workspace":"/repo","busy":false}}
{"type":"pong","id":"1"}
{"type":"state","id":"2","data":{"provider":"openai","model":"gpt-4.1-mini","workspace":"/repo","busy":false}}
{"type":"started","id":"3"}
{"type":"assistant_delta","id":"3","delta":"Hi"}
{"type":"assistant_done","id":"3","message":"Hi"}
{"type":"done","id":"3"}
```

## Stability notes

The protocol is intentionally small. The current contract is stable enough for:
- bots/bridges
- supervisors
- shell-driven integrations
- personal VPS harness use

If new event types are added later, existing event meanings should remain compatible.
