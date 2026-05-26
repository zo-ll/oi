package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ilog "github.com/zo-ll/oi/internal/log"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

// Runtime is the core agent runtime boundary.
type Runtime struct {
	Provider     provider.Provider
	Tools        *tool.Registry
	Policy       workspace.Policy
	Session      *session.Session
	MaxSteps     int
	ToolTimeout  time.Duration
	SystemPrompt string
	OnToolStart  func(tool.Call)
	OnToolResult func(tool.Call, tool.Result)
	Logger       *ilog.Logger
}

// RunOnce executes one user request through the bounded agent loop.
func (r *Runtime) RunOnce(ctx context.Context, input string) (string, error) {
	return r.run(ctx, input, nil, false)
}

// RunOnceStream executes one user request and forwards text deltas as they arrive.
func (r *Runtime) RunOnceStream(ctx context.Context, input string, onDelta func(string)) (string, error) {
	return r.run(ctx, input, onDelta, true)
}

func (r *Runtime) run(ctx context.Context, input string, onDelta func(string), streaming bool) (string, error) {
	if r == nil {
		return "", errors.New("nil runtime")
	}
	if r.Provider == nil {
		return "", errors.New("provider not configured")
	}
	if stringsTrim(input) == "" {
		return "", errors.New("input is required")
	}
	if r.MaxSteps <= 0 {
		r.MaxSteps = 12
	}
	if r.ToolTimeout <= 0 {
		r.ToolTimeout = 20 * time.Second
	}
	if r.Session == nil {
		r.Session = session.New(r.Provider.Name(), r.Provider.Model(), r.Policy.Root)
	}

	history := r.historyToProviderMessages()
	if len(history) == 0 {
		history = append(history, provider.Message{Role: "system", Content: r.systemPrompt()})
	}
	history = append(history, provider.Message{Role: "user", Content: input})
	r.Session.Messages = append(r.Session.Messages, session.Message{Role: "user", Content: input, Kind: "talk"})
	r.logEvent("user_input", map[string]any{"input": input})

	for step := 0; step < r.MaxSteps; step++ {
		r.logEvent("provider_request", map[string]any{"step": step + 1, "streaming": streaming, "message_count": len(history)})
		resp, err := r.callProvider(ctx, history, onDelta, streaming)
		if err != nil {
			r.logEvent("provider_error", map[string]any{"step": step + 1, "error": err.Error()})
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				err := fmt.Errorf("provider returned neither content nor tool calls")
				r.logEvent("provider_error", map[string]any{"step": step + 1, "error": err.Error()})
				return "", err
			}
			history = append(history, provider.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning})
			r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning, Kind: "talk"})
			r.Session.UpdatedAt = time.Now().UTC()
			r.logEvent("assistant_final", map[string]any{"step": step + 1, "content": resp.Content})
			return resp.Content, nil
		}

		assistantMsg := provider.Message{Role: "assistant", ToolCalls: resp.ToolCalls, Reasoning: resp.Reasoning}
		if resp.Content != "" {
			assistantMsg.Content = resp.Content
		}
		history = append(history, assistantMsg)
		toolCallJSON, _ := json.Marshal(resp.ToolCalls)
		r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: string(toolCallJSON), Reasoning: resp.Reasoning, Kind: "tool_call"})

		for _, tc := range resp.ToolCalls {
			call := tool.Call{ID: tc.ID, Name: tc.Name, Args: tc.Args}
			r.logEvent("tool_start", map[string]any{"step": step + 1, "tool": call.Name, "args": jsonRaw(call.Args)})
			if r.OnToolStart != nil {
				r.OnToolStart(call)
			}
			toolCtx, cancel := context.WithTimeout(ctx, r.ToolTimeout)
			res := r.Tools.Run(toolCtx, call)
			cancel()
			if r.OnToolResult != nil {
				r.OnToolResult(call, res)
			}
			r.logEvent("tool_result", map[string]any{"step": step + 1, "tool": call.Name, "ok": res.OK, "error": res.Error})

			payload, err := json.Marshal(res)
			if err != nil {
				payload = []byte(fmt.Sprintf(`{"tool":%q,"ok":false,"error":%q}`, tc.Name, err.Error()))
			}
			history = append(history, provider.Message{Role: "tool", ToolCallID: tc.ID, Content: string(payload)})
			r.Session.Messages = append(r.Session.Messages, session.Message{Role: "tool", ToolCallID: tc.ID, Content: string(payload), Kind: "tool_result"})
		}
	}

	err := fmt.Errorf("max steps exceeded (%d)", r.MaxSteps)
	r.logEvent("agent_error", map[string]any{"error": err.Error()})
	return "", err
}

func (r *Runtime) logEvent(kind string, fields map[string]any) {
	if r == nil || r.Logger == nil {
		return
	}
	_ = r.Logger.Event(kind, fields)
}

func jsonRaw(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return string(raw)
}

func (r *Runtime) callProvider(ctx context.Context, history []provider.Message, onDelta func(string), streaming bool) (provider.Response, error) {
	req := provider.Request{Model: r.Provider.Model(), Messages: history, Tools: providerToolSpecs(r.Tools)}
	if !streaming {
		return r.Provider.Chat(ctx, req)
	}
	stream, err := r.Provider.ChatStream(ctx, req)
	if err != nil {
		return provider.Response{}, err
	}
	var resp provider.Response
	for ev := range stream {
		if ev.Err != nil {
			return provider.Response{}, ev.Err
		}
		if ev.Reasoning != "" {
			resp.Reasoning += ev.Reasoning
		}
		if ev.Delta != "" {
			resp.Content += ev.Delta
			if onDelta != nil {
				onDelta(ev.Delta)
			}
		}
		if ev.ToolCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, *ev.ToolCall)
		}
		if ev.Done {
			break
		}
	}
	return resp, nil
}

func providerToolSpecs(reg *tool.Registry) []provider.ToolSpec {
	specs := reg.Specs()
	out := make([]provider.ToolSpec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, provider.ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
		})
	}
	return out
}

func (r *Runtime) historyToProviderMessages() []provider.Message {
	if r.Session == nil {
		return nil
	}
	var out []provider.Message
	for _, m := range r.Session.Messages {
		switch m.Kind {
		case "tool_call":
			var calls []provider.ToolCall
			if json.Unmarshal([]byte(m.Content), &calls) == nil {
				out = append(out, provider.Message{Role: "assistant", Reasoning: m.Reasoning, ToolCalls: calls})
			}
		case "tool_result":
			out = append(out, provider.Message{Role: "tool", ToolCallID: m.ToolCallID, Content: m.Content})
		default:
			out = append(out, provider.Message{Role: m.Role, Content: m.Content, Reasoning: m.Reasoning})
		}
	}
	return out
}

func (r *Runtime) systemPrompt() string {
	if stringsTrim(r.SystemPrompt) != "" {
		return r.SystemPrompt
	}
	return `You are oi, a careful coding agent.

Rules:
- Use tools when repository facts are needed.
- Prefer read-only inspection before mutation.
- Never invent file contents or command results.
- When editing, make the smallest reasonable change.
- Return a normal final answer once the task is complete.`
}

func stringsTrim(s string) string {
	return string(bytesTrimSpace([]byte(s)))
}

func bytesTrimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}
