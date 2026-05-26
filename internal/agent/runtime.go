package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
}

// RunOnce executes one user request through the bounded agent loop.
func (r *Runtime) RunOnce(ctx context.Context, input string) (string, error) {
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

	for step := 0; step < r.MaxSteps; step++ {
		resp, err := r.Provider.Chat(ctx, provider.Request{
			Model:    r.Provider.Model(),
			Messages: history,
			Tools:    providerToolSpecs(r.Tools),
		})
		if err != nil {
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				return "", fmt.Errorf("provider returned neither content nor tool calls")
			}
			history = append(history, provider.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning})
			r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning, Kind: "talk"})
			r.Session.UpdatedAt = time.Now().UTC()
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
			toolCtx, cancel := context.WithTimeout(ctx, r.ToolTimeout)
			res := r.Tools.Run(toolCtx, tool.Call{ID: tc.ID, Name: tc.Name, Args: tc.Args})
			cancel()

			payload, err := json.Marshal(res)
			if err != nil {
				payload = []byte(fmt.Sprintf(`{"tool":%q,"ok":false,"error":%q}`, tc.Name, err.Error()))
			}
			history = append(history, provider.Message{Role: "tool", ToolCallID: tc.ID, Content: string(payload)})
			r.Session.Messages = append(r.Session.Messages, session.Message{Role: "tool", Content: string(payload), Kind: "tool_result"})
		}
	}

	return "", fmt.Errorf("max steps exceeded (%d)", r.MaxSteps)
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
			var tc struct {
				Tool string `json:"tool"`
			}
			var toolCallID string
			_ = tc
			// persisted sessions are not yet resumed into active tool-call chains.
			out = append(out, provider.Message{Role: "tool", ToolCallID: toolCallID, Content: m.Content})
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
