package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ilog "github.com/zo-ll/oi/internal/log"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/retrieval"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

const defaultAgentStepLimit = 96

// Runtime is the core agent runtime boundary.
type Runtime struct {
	Provider                provider.Provider
	Tools                   *tool.Registry
	Policy                  workspace.Policy
	Session                 *session.Session
	MaxSteps                int
	ToolTimeout             time.Duration
	RequestTimeout          time.Duration
	ContextWindow           int
	ThinkingLevel           string
	ThinkingValue           string
	ThinkingFormat          string
	ThinkingSupported       bool
	SupportedThinkingLevels []string
	ThinkingLevelValues     map[string]string
	LastUsage               provider.Usage
	RecentRetrievedPaths    []string
	SystemPrompt            string
	OnRetrieve              func(retrieval.Notice)
	OnModelStart            func()
	OnModelStop             func()
	OnToolStart             func(tool.Call)
	OnToolResult            func(tool.Call, tool.Result)
	Logger                  *ilog.Logger
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
		r.MaxSteps = defaultAgentStepLimit
	}
	if r.ToolTimeout <= 0 {
		r.ToolTimeout = 20 * time.Second
	}
	if r.RequestTimeout <= 0 {
		r.RequestTimeout = 10 * time.Minute
	}
	if r.Session == nil {
		r.Session = session.New(r.Provider.Name(), r.Provider.Model(), r.Policy.Root)
	}

	retrievalContext, notice := r.buildRetrievalContext(input)
	r.Session.Messages = append(r.Session.Messages, session.Message{Role: "user", Content: input, Kind: "talk"})
	r.logEvent("user_input", map[string]any{"input": input, "retrieved_snippets": notice.SnippetCount, "retrieved_files": notice.FileCount})

	lastToolPlan := ""
	repeatedToolPlan := 0
	lastToolErr := ""
	repeatedToolErr := 0

	for step := 0; step < r.MaxSteps; step++ {
		history := r.providerHistory(retrievalContext)
		r.logEvent("provider_request", map[string]any{"step": step + 1, "streaming": streaming, "message_count": len(history)})
		deltaForward := onDelta
		if r.OnModelStart != nil {
			r.OnModelStart()
		}
		resp, err := r.callProvider(ctx, history, deltaForward, streaming)
		if r.OnModelStop != nil {
			r.OnModelStop()
		}
		if err != nil {
			r.logEvent("provider_error", map[string]any{"step": step + 1, "error": err.Error()})
			return "", err
		}

		resp.Content, resp.Reasoning = normalizeTaggedOutput(resp.Content, resp.Reasoning)
		if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
			r.LastUsage = resp.Usage
		}
		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				err := fmt.Errorf("provider returned neither content nor tool calls")
				r.logEvent("provider_error", map[string]any{"step": step + 1, "error": err.Error()})
				return "", err
			}
			r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning, Kind: "talk"})
			r.Session.UpdatedAt = time.Now().UTC()
			r.logEvent("assistant_final", map[string]any{"step": step + 1, "content": resp.Content})
			return resp.Content, nil
		}

		for i := range resp.ToolCalls {
			if resp.ToolCalls[i].ID == "" {
				resp.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", step+1, i+1)
			}
		}
		planSig := toolCallsSignature(resp.ToolCalls)
		if planSig != "" && planSig == lastToolPlan {
			repeatedToolPlan++
		} else {
			lastToolPlan = planSig
			repeatedToolPlan = 1
		}
		if repeatedToolPlan >= 3 {
			err := fmt.Errorf("stalled: repeated identical tool calls")
			r.logEvent("agent_error", map[string]any{"error": err.Error(), "step": step + 1})
			return "", err
		}
		r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: resp.Content, Reasoning: resp.Reasoning, Kind: "tool_call", ToolCalls: providerCallsToSession(resp.ToolCalls)})

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

			errSig := ""
			if !res.OK {
				errSig = call.Name + "\n" + string(call.Args) + "\n" + res.Error
				if errSig == lastToolErr {
					repeatedToolErr++
				} else {
					lastToolErr = errSig
					repeatedToolErr = 1
				}
				if repeatedToolErr >= 2 {
					err := fmt.Errorf("stalled: repeated identical tool error")
					r.logEvent("agent_error", map[string]any{"error": err.Error(), "step": step + 1})
					return "", err
				}
			} else {
				lastToolErr = ""
				repeatedToolErr = 0
			}

			payload, err := json.Marshal(res)
			if res.OK {
				r.rememberToolPaths(res)
			}
			if err != nil {
				payload = []byte(fmt.Sprintf(`{"tool":%q,"ok":false,"error":%q}`, tc.Name, err.Error()))
			}
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

func toolCallsSignature(calls []provider.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, call.Name+"\n"+string(call.Args))
	}
	return strings.Join(parts, "\n---\n")
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
	thinkingLevel := ""
	thinkingValue := ""
	thinkingFormat := ""
	if r.ThinkingSupported {
		thinkingLevel = r.ThinkingLevel
		thinkingValue = r.ThinkingValue
		thinkingFormat = r.ThinkingFormat
	}
	req := provider.Request{Model: r.Provider.Model(), Messages: history, Tools: providerToolSpecs(r.Tools), ThinkingLevel: thinkingLevel, ThinkingValue: thinkingValue, ThinkingFormat: thinkingFormat}
	requestCtx, cancel := context.WithTimeout(ctx, r.RequestTimeout)
	defer cancel()
	if !streaming {
		return r.Provider.Chat(requestCtx, req)
	}
	stream, err := r.Provider.ChatStream(requestCtx, req)
	if err != nil {
		return provider.Response{}, err
	}
	var resp provider.Response
	for {
		var ev provider.Event
		var ok bool
		select {
		case <-requestCtx.Done():
			return provider.Response{}, requestCtx.Err()
		case ev, ok = <-stream:
			if !ok {
				return resp, nil
			}
		}
		if ev.Err != nil {
			return provider.Response{}, ev.Err
		}
		if ev.Reasoning != "" {
			resp.Reasoning += ev.Reasoning
		}
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			resp.Usage = ev.Usage
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
	if len(resp.ToolCalls) == 0 {
		content, calls, ok := provider.ParseContentEnvelopeForCompatibility(stringsTrim(resp.Content))
		if ok {
			resp.Content = content
			resp.ToolCalls = calls
		}
	}
	return resp, nil
}

func (r *Runtime) providerHistory(retrievalContext string) []provider.Message {
	history := r.historyToProviderMessages()
	out := make([]provider.Message, 0, len(history)+2)
	out = append(out, provider.Message{Role: "system", Content: r.systemPrompt()})
	if stringsTrim(retrievalContext) != "" {
		out = append(out, provider.Message{Role: "system", Content: retrievalContext})
	}
	out = append(out, history...)
	return out
}

func (r *Runtime) buildRetrievalContext(input string) (string, retrieval.Notice) {
	notice := retrieval.Notice{Query: input, Skipped: true}
	if r == nil || r.Policy.Root == "" {
		return "", notice
	}
	contextText, note, err := retrieval.BuildContext(r.Policy.Root, input, r.recentPaths())
	if err != nil {
		r.logEvent("retrieval_error", map[string]any{"error": err.Error()})
		return "", notice
	}
	if len(note.Paths) > 0 {
		r.rememberRetrievedPaths(note.Paths)
	}
	if note.SnippetCount > 0 && r.OnRetrieve != nil {
		r.OnRetrieve(note)
	}
	if note.SnippetCount > 0 {
		r.logEvent("retrieval", map[string]any{"snippets": note.SnippetCount, "files": note.FileCount, "bytes": note.Bytes})
	}
	return contextText, note
}

func (r *Runtime) rememberRetrievedPaths(paths []string) {
	for _, path := range paths {
		path = stringsTrim(path)
		if path == "" {
			continue
		}
		r.RecentRetrievedPaths = append([]string{path}, filterOut(r.RecentRetrievedPaths, path)...)
		if len(r.RecentRetrievedPaths) > 8 {
			r.RecentRetrievedPaths = r.RecentRetrievedPaths[:8]
		}
	}
}

func (r *Runtime) rememberToolPaths(res tool.Result) {
	if r == nil || len(res.Meta) == 0 {
		return
	}
	for _, key := range []string{"path", "cwd"} {
		if path := stringsTrim(res.Meta[key]); path != "" {
			r.rememberRetrievedPaths([]string{path})
		}
	}
}

func (r *Runtime) recentPaths() []string {
	if r == nil {
		return nil
	}
	paths := append([]string(nil), r.RecentRetrievedPaths...)
	if r.Session == nil {
		return paths
	}
	for i := len(r.Session.Messages) - 1; i >= 0 && len(paths) < 12; i-- {
		m := r.Session.Messages[i]
		if m.Kind != "tool_result" || stringsTrim(m.Content) == "" {
			continue
		}
		var res tool.Result
		if json.Unmarshal([]byte(m.Content), &res) != nil {
			continue
		}
		for _, key := range []string{"path", "cwd"} {
			if path := stringsTrim(res.Meta[key]); path != "" {
				paths = append(paths, path)
			}
		}
	}
	return uniqueStrings(paths, 12)
}

func filterOut(items []string, target string) []string {
	var out []string
	for _, item := range items {
		if item != target {
			out = append(out, item)
		}
	}
	return out
}

func uniqueStrings(items []string, limit int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, item := range items {
		item = stringsTrim(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (r *Runtime) ForceCompactSession() (bool, int) {
	if r == nil || r.Session == nil {
		return false, 0
	}
	budget := r.compactionBudget()
	compacted, changed := session.ForceCompactMessages(r.Session.Messages)
	if !changed {
		return false, budget
	}
	r.Session.Messages = compacted
	r.logEvent("session_compacted", map[string]any{"message_count": len(compacted), "budget_tokens": budget, "forced": true})
	return true, budget
}

func (r *Runtime) CompactSession() (bool, int) {
	if r == nil || r.Session == nil {
		return false, 0
	}
	budget := r.compactionBudget()
	compacted, changed := session.CompactMessages(r.Session.Messages, budget)
	if !changed {
		return false, budget
	}
	r.Session.Messages = compacted
	r.logEvent("session_compacted", map[string]any{"message_count": len(compacted), "budget_tokens": budget})
	return true, budget
}

func (r *Runtime) compactionBudget() int {
	window := r.contextWindow()
	if window > 0 {
		budget := window * 70 / 100
		if budget > 0 {
			return budget
		}
	}
	return 24000
}

func (r *Runtime) contextWindow() int {
	if r == nil || r.Provider == nil {
		return 0
	}
	if r.ContextWindow > 0 {
		return r.ContextWindow
	}
	model := r.Provider.Model()
	if stringsTrim(model) == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := r.Provider.ListModels(ctx)
	if err != nil {
		return 0
	}
	for _, item := range models {
		if item.ID == model {
			r.ContextWindow = item.ContextWindow
			return r.ContextWindow
		}
	}
	return 0
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
		case "summary":
			out = append(out, provider.Message{Role: "system", Content: m.Content})
		case "tool_call":
			calls := sessionCallsToProvider(m.ToolCalls)
			if len(calls) == 0 {
				// Backward compatibility with sessions saved before tool_calls was
				// a first-class field: content contained the serialized calls.
				_ = json.Unmarshal([]byte(m.Content), &calls)
			}
			out = append(out, provider.Message{Role: "assistant", Content: contentForToolCallMessage(m, calls), Reasoning: m.Reasoning, ToolCalls: calls})
		case "tool_result":
			out = append(out, provider.Message{Role: "tool", ToolCallID: m.ToolCallID, Content: m.Content})
		default:
			out = append(out, provider.Message{Role: m.Role, Content: m.Content, Reasoning: m.Reasoning})
		}
	}
	return out
}

func providerCallsToSession(calls []provider.ToolCall) []session.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, session.ToolCall{ID: call.ID, Name: call.Name, Args: json.RawMessage(append([]byte(nil), call.Args...))})
	}
	return out
}

func sessionCallsToProvider(calls []session.ToolCall) []provider.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, provider.ToolCall{ID: call.ID, Name: call.Name, Args: json.RawMessage(append([]byte(nil), call.Args...))})
	}
	return out
}

func contentForToolCallMessage(m session.Message, calls []provider.ToolCall) string {
	if len(m.ToolCalls) > 0 {
		return m.Content
	}
	if len(calls) > 0 {
		return ""
	}
	return m.Content
}

type outputSegment struct {
	Text      string
	Reasoning bool
}

func splitTaggedOutput(text string) []outputSegment {
	var out []outputSegment
	for len(text) > 0 {
		start := strings.Index(text, "<think>")
		if start < 0 {
			appendOutputSegment(&out, text, false)
			break
		}
		appendOutputSegment(&out, text[:start], false)
		text = text[start+len("<think>"):]
		end := strings.Index(text, "</think>")
		if end < 0 {
			appendOutputSegment(&out, text, true)
			break
		}
		appendOutputSegment(&out, text[:end], true)
		text = text[end+len("</think>"):]
	}
	return out
}

func normalizeTaggedOutput(content, reasoning string) (string, string) {
	segments := splitTaggedOutput(content)
	if len(segments) == 0 {
		return strings.TrimSpace(content), strings.TrimSpace(reasoning)
	}
	var visible []string
	var hidden []string
	if strings.TrimSpace(reasoning) != "" {
		hidden = append(hidden, strings.TrimSpace(reasoning))
	}
	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		if seg.Reasoning {
			hidden = append(hidden, text)
		} else {
			visible = append(visible, text)
		}
	}
	return strings.Join(visible, "\n\n"), strings.Join(hidden, "\n\n")
}

func appendOutputSegment(out *[]outputSegment, text string, reasoning bool) {
	if text == "" {
		return
	}
	if n := len(*out); n > 0 && (*out)[n-1].Reasoning == reasoning {
		(*out)[n-1].Text += text
		return
	}
	*out = append(*out, outputSegment{Text: text, Reasoning: reasoning})
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
- Prefer plain text over markdown unless the user asks for markdown.
- Return normal UTF-8 text once the task is complete.
- End every response with a complete sentence; do not leave dangling punctuation.`
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
