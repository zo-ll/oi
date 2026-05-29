package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultCodexBaseURL = "https://chatgpt.com/backend-api"

var codexModels = []Model{
	// The ChatGPT Codex backend does not expose the normal OpenAI
	// Platform /v1/models endpoint, so keep a broad built-in picker list.
	// Some accounts may not have access to every model here.
	{ID: "gpt-5-codex", Name: "GPT-5 Codex"},
	{ID: "gpt-5.1-codex", Name: "GPT-5.1 Codex"},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
	{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark"},
	{ID: "gpt-5", Name: "GPT-5"},
	{ID: "gpt-5-mini", Name: "GPT-5 mini"},
	{ID: "gpt-5-nano", Name: "GPT-5 nano"},
	{ID: "gpt-5.1", Name: "GPT-5.1"},
	{ID: "gpt-5.1-mini", Name: "GPT-5.1 mini"},
	{ID: "gpt-5.2", Name: "GPT-5.2"},
	{ID: "gpt-5.4", Name: "GPT-5.4"},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 mini"},
	{ID: "gpt-5.5", Name: "GPT-5.5"},
	{ID: "gpt-4.1", Name: "GPT-4.1"},
	{ID: "gpt-4.1-mini", Name: "GPT-4.1 mini"},
	{ID: "gpt-4.1-nano", Name: "GPT-4.1 nano"},
	{ID: "gpt-4o", Name: "GPT-4o"},
	{ID: "gpt-4o-mini", Name: "GPT-4o mini"},
	{ID: "o3", Name: "o3"},
	{ID: "o3-mini", Name: "o3 mini"},
	{ID: "o4-mini", Name: "o4 mini"},
}

// OpenAICodexProvider implements ChatGPT Codex Responses API access.
type OpenAICodexProvider struct {
	name      string
	baseURL   string
	apiKey    string
	accountID string
	model     string
	client    *http.Client
}

// NewOpenAICodex constructs a ChatGPT Codex OAuth-backed provider.
func NewOpenAICodex(name, baseURL, apiKey, accountID, model string) (*OpenAICodexProvider, error) {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if baseURL == "" {
		baseURL = defaultCodexBaseURL
	}
	if name == "" {
		name = "openai-codex"
	}
	apiKey = strings.TrimSpace(strings.TrimPrefix(apiKey, "Bearer "))
	if apiKey == "" {
		return nil, fmt.Errorf("no OAuth access token resolved for provider %q", name)
	}
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("missing ChatGPT account id for provider %q", name)
	}
	return &OpenAICodexProvider{
		name:      name,
		baseURL:   baseURL,
		apiKey:    apiKey,
		accountID: strings.TrimSpace(accountID),
		model:     strings.TrimSpace(model),
		client:    &http.Client{Timeout: 0},
	}, nil
}

func (p *OpenAICodexProvider) Name() string  { return p.name }
func (p *OpenAICodexProvider) Model() string { return p.model }
func (p *OpenAICodexProvider) SetModel(model string) {
	p.model = model
}

func (p *OpenAICodexProvider) Chat(ctx context.Context, req Request) (Response, error) {
	var out Response
	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return Response{}, err
	}
	for ev := range stream {
		if ev.Err != nil {
			return Response{}, ev.Err
		}
		if ev.Reasoning != "" {
			out.Reasoning += ev.Reasoning
		}
		if ev.Delta != "" {
			out.Content += ev.Delta
		}
		if ev.ToolCall != nil {
			out.ToolCalls = append(out.ToolCalls, *ev.ToolCall)
		}
	}
	return out, nil
}

func (p *OpenAICodexProvider) ChatStream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveCodexURL(p.baseURL), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for k, v := range p.headers() {
		httpReq.Header.Set(k, v)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, data)
	}
	ch := make(chan Event, 16)
	go p.readStream(resp.Body, ch)
	return ch, nil
}

func (p *OpenAICodexProvider) ListModels(context.Context) ([]Model, error) {
	return append([]Model(nil), codexModels...), nil
}

func (p *OpenAICodexProvider) buildRequest(req Request) (map[string]any, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(p.model)
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	instructions := make([]string, 0, 1)
	items := make([]any, 0, len(req.Messages))
	toolIndex := 0
	textIndex := 0
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			if strings.TrimSpace(m.Content) != "" {
				instructions = append(instructions, m.Content)
			}
		case "user":
			items = append(items, map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": m.Content}},
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				items = append(items, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"status":  "completed",
					"id":      fmt.Sprintf("msg_%d", textIndex),
					"content": []any{map[string]any{"type": "output_text", "text": m.Content, "annotations": []any{}}},
				})
				textIndex++
			}
			for _, tc := range m.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					callID = fmt.Sprintf("call_%d", toolIndex)
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"id":        fmt.Sprintf("fc_%d", toolIndex),
					"call_id":   sanitizeCodexID(callID),
					"name":      tc.Name,
					"arguments": string(normalizeArgs(tc.Args)),
				})
				toolIndex++
			}
		case "tool":
			callID := strings.TrimSpace(m.ToolCallID)
			if callID == "" {
				callID = fmt.Sprintf("call_%d", toolIndex)
			}
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": sanitizeCodexID(callID),
				"output":  m.Content,
			})
			toolIndex++
		}
	}
	body := map[string]any{
		"model":               model,
		"store":               false,
		"stream":              true,
		"instructions":        strings.TrimSpace(strings.Join(instructions, "\n\n")),
		"input":               items,
		"text":                map[string]any{"verbosity": "low"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	if body["instructions"] == "" {
		body["instructions"] = "You are oi, a careful coding assistant."
	}
	if len(req.Tools) > 0 {
		body["tools"] = toResponsesTools(req.Tools)
	}
	return body, nil
}

func toResponsesTools(specs []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(spec.InputSchema) > 0 {
			var decoded any
			if json.Unmarshal(spec.InputSchema, &decoded) == nil {
				if m, ok := decoded.(map[string]any); ok {
					params = m
				}
			}
		}
		out = append(out, map[string]any{
			"type":        "function",
			"name":        spec.Name,
			"description": spec.Description,
			"parameters":  params,
			"strict":      false,
		})
	}
	return out
}

func (p *OpenAICodexProvider) headers() map[string]string {
	return map[string]string{
		"Authorization":      "Bearer " + p.apiKey,
		"chatgpt-account-id": p.accountID,
		"originator":         "oi",
		"OpenAI-Beta":        "responses=experimental",
		"Accept":             "text/event-stream",
		"Content-Type":       "application/json",
		"User-Agent":         "oi/0",
	}
}

func resolveCodexURL(baseURL string) string {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if baseURL == "" {
		baseURL = defaultCodexBaseURL
	}
	if strings.HasSuffix(baseURL, "/codex/responses") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/codex") {
		return baseURL + "/responses"
	}
	return baseURL + "/codex/responses"
}

func sanitizeCodexID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "call"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		return "call"
	}
	return out
}

type codexEvent struct {
	Type         string `json:"type"`
	Delta        string `json:"delta,omitempty"`
	Text         string `json:"text,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	OutputIndex  int    `json:"output_index,omitempty"`
	ContentIndex int    `json:"content_index,omitempty"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	Response     *struct {
		Status string `json:"status,omitempty"`
		Usage  *struct {
			InputTokens  int `json:"input_tokens,omitempty"`
			OutputTokens int `json:"output_tokens,omitempty"`
		} `json:"usage,omitempty"`
		Error *struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"error,omitempty"`
	} `json:"response,omitempty"`
	Item *struct {
		Type      string `json:"type,omitempty"`
		ID        string `json:"id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
		Content   []struct {
			Type    string `json:"type,omitempty"`
			Text    string `json:"text,omitempty"`
			Refusal string `json:"refusal,omitempty"`
		} `json:"content,omitempty"`
		Summary []struct {
			Text string `json:"text,omitempty"`
		} `json:"summary,omitempty"`
	} `json:"item,omitempty"`
}

type codexStreamState struct {
	pendingCalls map[string]*pendingCodexCall
	callOrder    []string
	currentCall  string
	emittedText  bool
}

type pendingCodexCall struct {
	id   string
	name string
	args strings.Builder
}

func (p *OpenAICodexProvider) readStream(body io.ReadCloser, ch chan<- Event) {
	defer close(ch)
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var block []string
	state := &codexStreamState{pendingCalls: make(map[string]*pendingCodexCall)}
	done := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			finished, err := emitCodexBlock(ch, block, state)
			if err != nil {
				ch <- Event{Err: err}
				return
			}
			if finished {
				done = true
				return
			}
			block = block[:0]
			continue
		}
		block = append(block, line)
	}
	if err := scanner.Err(); err != nil {
		ch <- Event{Err: err}
		return
	}
	if !done {
		finished, err := emitCodexBlock(ch, block, state)
		if err != nil {
			ch <- Event{Err: err}
			return
		}
		if !finished {
			ch <- Event{Type: EventDone, Done: true}
		}
	}
}

func emitCodexBlock(ch chan<- Event, lines []string, state *codexStreamState) (bool, error) {
	if len(lines) == 0 {
		return false, nil
	}
	var dataLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
	}
	if len(dataLines) == 0 {
		return false, nil
	}
	payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		emitPendingCodexCalls(ch, state)
		ch <- Event{Type: EventDone, Done: true}
		return true, nil
	}
	var ev codexEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return false, fmt.Errorf("parse codex stream event: %w", err)
	}
	switch ev.Type {
	case "error":
		if ev.Message != "" || ev.Code != "" {
			return false, fmt.Errorf("codex error: %s %s", ev.Code, ev.Message)
		}
		return false, fmt.Errorf("codex error")
	case "response.failed":
		if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
			return false, fmt.Errorf("codex error: %s", ev.Response.Error.Message)
		}
		return false, fmt.Errorf("codex response failed")
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if ev.Delta != "" {
			ch <- Event{Type: EventDelta, Reasoning: ev.Delta}
		}
	case "response.output_text.delta", "response.refusal.delta":
		if ev.Delta != "" {
			state.emittedText = true
			ch <- Event{Type: EventDelta, Delta: ev.Delta}
		}
	case "response.output_text.done", "response.refusal.done":
		if !state.emittedText && ev.Text != "" {
			state.emittedText = true
			ch <- Event{Type: EventDelta, Delta: ev.Text}
		}
	case "response.output_item.added":
		if ev.Item != nil && ev.Item.Type == "function_call" {
			key := codexCallKey(ev, state)
			call := ensureCodexCall(state, key)
			if ev.Item.CallID != "" {
				call.id = ev.Item.CallID
			}
			if ev.Item.Name != "" {
				call.name = ev.Item.Name
			}
			call.args.WriteString(ev.Item.Arguments)
		}
	case "response.function_call_arguments.delta":
		key := codexCallKey(ev, state)
		if key != "" {
			ensureCodexCall(state, key).args.WriteString(ev.Delta)
		}
	case "response.function_call_arguments.done":
		key := codexCallKey(ev, state)
		if key != "" && ev.Arguments != "" {
			call := ensureCodexCall(state, key)
			call.args.Reset()
			call.args.WriteString(ev.Arguments)
		}
	case "response.output_item.done":
		if ev.Item != nil && ev.Item.Type == "function_call" {
			emitCodexCall(ch, state, codexCallKey(ev, state), ev.Item.CallID, ev.Item.Name, ev.Item.Arguments)
		}
		if ev.Item != nil && ev.Item.Type == "message" && !state.emittedText {
			if text := codexItemText(ev); text != "" {
				state.emittedText = true
				ch <- Event{Type: EventDelta, Delta: text}
			}
		}
	case "response.done", "response.completed", "response.incomplete":
		emitPendingCodexCalls(ch, state)
		ch <- Event{Type: EventDone, Done: true}
		return true, nil
	}
	return false, nil
}

func codexCallKey(ev codexEvent, state *codexStreamState) string {
	if ev.Item != nil {
		if ev.Item.ID != "" {
			return ev.Item.ID
		}
		if ev.Item.CallID != "" {
			return ev.Item.CallID
		}
	}
	if ev.ItemID != "" {
		return ev.ItemID
	}
	if state != nil && state.currentCall != "" {
		return state.currentCall
	}
	return ""
}

func ensureCodexCall(state *codexStreamState, key string) *pendingCodexCall {
	if key == "" {
		key = fmt.Sprintf("call_%d", len(state.callOrder)+1)
	}
	if state.pendingCalls == nil {
		state.pendingCalls = make(map[string]*pendingCodexCall)
	}
	if call := state.pendingCalls[key]; call != nil {
		return call
	}
	call := &pendingCodexCall{}
	state.pendingCalls[key] = call
	state.callOrder = append(state.callOrder, key)
	state.currentCall = key
	return call
}

func emitCodexCall(ch chan<- Event, state *codexStreamState, key, callID, name, args string) {
	if key == "" {
		key = state.currentCall
	}
	if call := state.pendingCalls[key]; call != nil {
		if callID == "" {
			callID = call.id
		}
		if name == "" {
			name = call.name
		}
		if strings.TrimSpace(call.args.String()) != "" {
			args = call.args.String()
		}
	}
	if name != "" {
		ch <- Event{Type: EventToolCall, ToolCall: &ToolCall{ID: callID, Name: name, Args: normalizeArgs([]byte(args))}}
	}
	delete(state.pendingCalls, key)
	if state.currentCall == key {
		state.currentCall = ""
	}
}

func emitPendingCodexCalls(ch chan<- Event, state *codexStreamState) {
	for _, key := range state.callOrder {
		if call := state.pendingCalls[key]; call != nil && call.name != "" {
			emitCodexCall(ch, state, key, call.id, call.name, call.args.String())
		}
	}
}

func codexItemText(ev codexEvent) string {
	if ev.Item == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range ev.Item.Content {
		switch part.Type {
		case "output_text", "text":
			b.WriteString(part.Text)
		case "refusal":
			b.WriteString(part.Refusal)
		}
	}
	return b.String()
}
