# RPC protocol

`oi rpc` speaks newline-delimited JSON over stdin/stdout.

## Requests

Each request is one JSON object with a `type` and optional `id`.

Supported request types:

- `ping`
- `get_state`
- `list_providers`
- `list_models`
- `set_provider`
- `set_model`
- `new_session`
- `abort`
- `prompt`

## Example

```bash
printf '{"id":"1","type":"ping"}\n{"id":"2","type":"get_state"}\n{"id":"3","type":"prompt","message":"say hi"}\n' | oi rpc
```

## Events

The server emits newline-delimited JSON events.

Common event types:

- `ready`
- `pong`
- `state`
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
- `session`

## Notes

- `prompt` is single-flight; only one active request runs at a time.
- `abort` cancels the active request if one is running.
- Tool activity is emitted as separate events.
- The protocol is intentionally small and easy to bridge from bots or shell scripts.
