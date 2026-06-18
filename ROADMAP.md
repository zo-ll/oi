# oi roadmap

This file tracks the current implementation roadmap. The original compile-only rebuild plan has mostly been completed; the project is now in stabilization/hardening with a larger feature backlog migrated from `oi-old` issues.

## Current status — 2026-06-17

Implemented and working in the new Go codebase:

- Clean package layout:
  - `cmd/oi`
  - `internal/config`
  - `internal/provider`
  - `internal/agent`
  - `internal/chat`
  - `internal/tool`
  - `internal/workspace`
  - `internal/rpc`
  - `internal/session`
  - `internal/log`
  - `internal/oauth`
  - `internal/retrieval`
  - `tide` (first-party terminal primitives dependency)
- Config/auth:
  - XDG config/state paths
  - `config.json` and private `auth.json`
  - provider profiles and env/auth precedence
  - `oi doctor`, `oi login`, `oi logout`
  - selected provider/model semantics (`selected_provider`, `selected_model`)
- Providers:
  - OpenAI-compatible `/v1/chat/completions`
  - streaming SSE parser
  - tool-call normalization
  - model listing for API providers
  - OpenCode Go wired through `opencode-go` with current `/zen/go/v1` profile and model-family dispatch for both `/chat/completions` and `/messages`
  - ChatGPT subscription/browser OAuth provider via `openai-codex`
- Workspace/policy:
  - root detection
  - path safety checks
  - approval modes (default: `auto`)
  - output truncation
  - per-tool timeouts
- Tools:
  - `read_file`
  - `list_dir`
  - `find_files`
  - `grep`
  - `run_command`
  - `write_file`
  - `replace_in_file`
- Agent loop:
  - bounded tool loop
  - internal-only step cap
  - anti-stall detection for repeated tool plans/errors
  - session history
  - request/tool timeout handling
  - streaming and non-streaming paths
- RPC:
  - NDJSON stdio framing
  - prompt/state/provider/model commands
  - events for streaming, tools, errors, done
- Interactive TUI (`internal/chat/tui*.go`, built on `tide`):
  - fullscreen alt-screen UI on a TTY, line-mode fallback otherwise
  - editable prompt with cursor movement, home/end, backspace, multiline
  - prompt history: up/down arrows recall previous inputs
  - slash command hints: type `/` to browse, arrows to navigate, tab to fill
  - arrow-key overlay pickers for `/model`, `/think`, `/stream`, `/tools`, `/autosave`, `/login`, `/session`
  - mouse wheel scrolls the transcript; shift-drag selects text
  - steering: type + enter while a turn is running queues a follow-up, injected at the next safe boundary (no hard abort)
  - approval modal for mutating actions when `approval_mode` is `prompt`
  - auto-compaction at 90% of the context window by default (configurable, can disable)
  - render caching for streaming output; cached terminal size refreshed on SIGWINCH
- Chat:
  - default `oi` mode
  - fullscreen TUI on a TTY (alt screen, overlays, mouse wheel), line-mode fallback otherwise
  - `/status` command for model/context/thinking/auto-compact/session info on demand
  - `/help`, `/status`, `/login`, `/model`, `/stream`, `/think`, `/tools`, `/autosave`, `/new`, `/save`, `/session`, `/compact`, `/clear`, `/exit`
  - `/login` flow: choose `sub` or `api`, then provider
  - `/model` interactive arrow-key picker
  - provider switching implicit through `/model`
- Sessions/logs:
  - rolling autosave
  - named snapshots
  - load/list/filter sessions
  - debug JSONL logs

Current active phase: **Phase 10 — hardening, cleanup, and real-world smoke testing**.

---

## Immediate next steps

1. **Commit current working changes** once the current behavior is accepted.
2. **Real-world smoke test ChatGPT subscription flow**:
   - `/login` → `sub` → `openai`
   - `/model`
   - one normal prompt
   - one prompt requiring tools
   - `/compact`
   - reload session and continue
3. **Fix only issues discovered by the smoke test**.
4. **Improve `oi doctor`** for browser/subscription login:
   - show OAuth token presence/expiry
   - show ChatGPT account id presence
   - distinguish `openai` API key from `openai-codex` subscription login
7. Keep local backend/TUI/RAG/image work deferred until v1 is stable.

---

## Phase 10 — hardening and cleanup — active

### Goals

- Make v1 solid before adding large new systems.
- Keep package boundaries clean.
- Keep the CLI predictable and safe.
- Make browser-login and API-key providers unambiguous.

### Tasks

- Review package boundaries.
- Split oversized chat command code.
- Improve error messages.
- Add integration tests for key flows.
- Improve `oi doctor`.
- Keep docs current.
- Make list/picker UX usable enough until a real picker/TUI lands.

### Done when

- v1 behavior is understandable from README/RPC docs/roadmap.
- Common login/model/session flows are tested.
- No core file is doing too many unrelated jobs.
- Browser-login OpenAI subscription path is clearly separate from OpenAI Platform API keys.

---

## Phase 11 — optional local backend — deferred

Only after cloud-first v1 is stable.

### Goals

- Support local OpenAI-compatible servers as an optional provider path.

### Tasks

- Support generic local OpenAI-compatible base URLs.
- Reuse the existing provider interface, agent loop, tools, sessions, and RPC.
- Do not add mandatory local inference dependencies to core `oi`.

---

# Backlog migrated from oi-old issues

The following plans came from open `oi-old` issues and should be considered post-v1 unless marked otherwise.

## Picker/list UX — done

### Status

Implemented as overlay pickers in `internal/chat/tui_overlay.go` (built on `tide`). The TUI provides:
- arrow-key selector with filter typing and scrolling
- slash command hints (type `/` to browse)
- reused by `/model`, `/think`, `/stream`, `/tools`, `/autosave`, `/login`, `/session`
- dim styling for overlay text
- line-mode numbered fallback outside a TTY

### `@` file fuzzy completion

Partially done: `@` file completion exists through the completion hook. Fuzzy file matching is available in `internal/chat/completion.go`.

---

## TUI — rich terminal interface

### Status

Built and shipped. The interactive mode is now a fullscreen TUI (alt screen, mouse-wheel scroll, overlay pickers, approval modal) implemented in `internal/chat/tui*.go` on top of `tide`. This supersedes the earlier shell-style line editor. The `internal/lineedit` package no longer exists; line-editing primitives live in `tide`.

### Future

Keep the TUI split by concern (`tui.go`, `tui_render.go`, `tui_input.go`, `tui_overlay.go`) and avoid growing `tui.go` back into a monolith. Heavier widgets, if ever needed, stay in `oi` rather than `tide` to keep `tide` minimal.

---

## RAG — Retrieval-Augmented Generation

### Source

- `oi-old` #15 — RAG for oi

### Goals

Index the workspace into chunks, retrieve relevant chunks per prompt, and inject them as context before provider calls.

### Why

- Reduces tool round-trips.
- Helps with large repos and small context windows.
- Reduces token cost on cloud providers.
- Reduces hallucinated file paths by giving relevant code context upfront.

### Proposed package

`internal/rag/`

| File | Purpose |
|------|---------|
| `rag.go` | Public API: index, retrieve, format context |
| `chunk.go` | File chunking, line-based first, AST-aware later |
| `store.go` | BM25 inverted index and JSONL persistence |
| `embed.go` | Embedder interface for later embedding search |
| `config.go` | RAG config |

### Slash commands

- `/rag index [dir]`
- `/rag on|off`
- `/rag status`
- `/rag topk N`

### Phased delivery

1. **BM25 RAG**
   - stdlib-only
   - line/section chunks
   - JSONL index
   - retrieve top-k before model request
2. **Embedding RAG**
   - embedder interface
   - local/API embeddings
   - cosine similarity
3. **Polish**
   - AST-aware chunking for Go
   - `.ragignore`
   - auto-index on project open
   - incremental re-index

---

## Image / vision support

### Source

- `oi-old` #18 — Image support: capture, process, send and display images in chat

### Goals

Allow oi to consume and send images to vision-capable models.

### Planned features

- Add image content to provider-neutral message types.
- Send images as base64 data URIs where supported.
- `/image <path>` slash command.
- `/screenshot` slash command.
- Screenshot capture by shelling out to available platform tools:
  - Linux Wayland: `grim`
  - X11/ImageMagick: `import`
  - later macOS/Windows equivalents
- Process images using Go stdlib where possible:
  - resize
  - compress
  - format conversion where supported
- Terminal display:
  - kitty protocol
  - sixel
  - ASCII fallback

### Notes

This requires provider interface changes, session format changes, and model capability handling. Defer until v1 text/tool flows are stable.

---

## Clipboard integration

### Source

- `oi-old` #19 — Clipboard read/write

### Planned features

- Paste from system clipboard into input.
- Copy output via keybinding instead of a slash command.
- Cross-platform tool detection:
  - `wl-paste` / `wl-copy`
  - `xclip` / `xsel`
  - `pbpaste` / `pbcopy`
  - PowerShell clipboard commands
- Graceful fallback when unavailable.

### TUI relationship

Clipboard support belongs naturally in the future TUI/input system but can be implemented earlier as small platform helpers.

---

## Security hardening

### Source

- `oi-old` #16 — Security: harden tool execution

### Current new-code status

The new `oi` already has stronger workspace/policy boundaries than `oi-old`, but security should remain an explicit hardening track.

### Tasks

- Review shell command policy.
- Prefer allowlists/safe patterns over fragile blocklists.
- Continue path traversal/symlink escape tests.
- Ensure all sensitive files use private permissions where appropriate.
- Ensure debug logs do not accidentally expose secrets.
- Confirm headless/RPC writes cannot bypass policy.

---

# Original rebuild phases — historical status

## Phase 0 — reset and freeze scope

Status: done.

## Phase 1 — project skeleton

Status: done.

## Phase 2 — config and auth

Status: done.

## Phase 3 — OpenAI-compatible provider

Status: done.

## Phase 4 — workspace and policy

Status: done.

## Phase 5 — tool system

Status: done.

## Phase 6 — agent loop

Status: done.

## Phase 7 — RPC mode

Status: done.

## Phase 8 — minimal interactive chat

Status: done.

## Phase 9 — sessions and logs

Status: mostly done.

## Phase 10 — hardening and cleanup

Status: active.

## Phase 11 — optional local backend

Status: deferred.

---

# What to copy conceptually from oi-old

Keep the ideas:

- provider abstraction
- RPC mode
- sessions
- logging
- streaming
- fuzzy picker UX
- future TUI direction
- RAG plan
- image/vision plan

Do not copy the shape:

- giant `agent.go`
- XML-ish tool tags as the main protocol
- REPL-heavy feature sprawl
- mixed UI/provider/agent concerns
- mandatory external dependencies for core interaction
