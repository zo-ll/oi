package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
type OpenAIProvider struct {
	name       string
	baseURL    string
	apiKey     string
	model      string
	client     *http.Client
	maxRetries int
}

// NewOpenAI constructs an OpenAI-compatible provider.
func NewOpenAI(name, baseURL, apiKey, model string) (*OpenAIProvider, error) {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if name == "" {
		name = "openai-compatible"
	}
	return &OpenAIProvider{
		name:       name,
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(strings.TrimPrefix(apiKey, "Bearer ")),
		model:      model,
		client:     &http.Client{Timeout: 60 * time.Second},
		maxRetries: 2,
	}, nil
}

func (p *OpenAIProvider) Name() string  { return p.name }
func (p *OpenAIProvider) Model() string { return p.model }
func (p *OpenAIProvider) SetModel(model string) {
	p.model = model
}

// Chat executes a non-streaming chat completion request.
func (p *OpenAIProvider) Chat(ctx context.Context, req Request) (Response, error) {
	body, err := p.buildRequest(req, false)
	if err != nil {
		return Response{}, err
	}
	data, err := p.doJSON(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	return parseChatResponse(data)
}

// ChatStream executes a streaming chat completion request.
func (p *OpenAIProvider) ChatStream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := p.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	client := *p.client
	client.Timeout = 0
	resp, err := client.Do(httpReq)
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

// ListModels returns models exposed by the provider.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	data, err := p.doJSON(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	models := make([]Model, 0, len(resp.Data))
	for _, m := range resp.Data {
		models = append(models, Model{ID: m.ID, Name: m.ID})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (p *OpenAIProvider) buildRequest(req Request, stream bool) (map[string]any, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	messages, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}
	if len(req.Tools) > 0 {
		body["tools"] = toOpenAITools(req.Tools)
	}
	return body, nil
}

func toOpenAIMessages(messages []Message) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"role": m.Role}
		if m.Content != "" {
			msg["content"] = m.Content
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": string(normalizeArgs(tc.Args)),
					},
				})
			}
			msg["tool_calls"] = calls
		}
		if _, ok := msg["content"]; !ok && m.Role != "assistant" {
			msg["content"] = ""
		}
		out = append(out, msg)
	}
	return out, nil
}

func toOpenAITools(specs []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(spec.InputSchema) > 0 {
			var decoded any
			if json.Unmarshal(spec.InputSchema, &decoded) == nil {
				params = map[string]any{}
				if m, ok := decoded.(map[string]any); ok {
					params = m
				}
			}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

func (p *OpenAIProvider) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}

	attempts := p.maxRetries + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := p.newRequest(ctx, method, path, payload)
		if err != nil {
			return nil, err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			if i < attempts-1 {
				continue
			}
			break
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if i < attempts-1 {
				continue
			}
			break
		}
		if resp.StatusCode >= 500 && i < attempts-1 {
			lastErr = parseAPIError(resp.StatusCode, data)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, parseAPIError(resp.StatusCode, data)
		}
		return data, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("request failed")
	}
	return nil, lastErr
}

func (p *OpenAIProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if seeker, ok := body.(io.Seeker); ok {
		_, _ = seeker.Seek(0, io.SeekStart)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	return req, nil
}

func (p *OpenAIProvider) readStream(body io.ReadCloser, ch chan<- Event) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	pending := map[int]*streamToolCall{}
	emitted := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			if !emitted {
				emitPendingTools(ch, pending)
			}
			ch <- Event{Type: EventDone, Done: true}
			return
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- Event{Err: fmt.Errorf("parse stream chunk: %w", err)}
			return
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				ch <- Event{Type: EventDelta, Delta: choice.Delta.Content}
			}
			for _, call := range choice.Delta.ToolCalls {
				cur := pending[call.Index]
				if cur == nil {
					cur = &streamToolCall{}
					pending[call.Index] = cur
				}
				if call.ID != "" {
					cur.ID = call.ID
				}
				if call.Function.Name != "" {
					cur.Name = call.Function.Name
				}
				cur.Args.WriteString(call.Function.Arguments)
			}
			if choice.FinishReason == "tool_calls" {
				emitPendingTools(ch, pending)
				emitted = true
			}
			if choice.FinishReason == "stop" {
				ch <- Event{Type: EventDone, Done: true}
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- Event{Err: err}
		return
	}
	if !emitted {
		emitPendingTools(ch, pending)
	}
	ch <- Event{Type: EventDone, Done: true}
}

type streamToolCall struct {
	ID   string
	Name string
	Args strings.Builder
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func emitPendingTools(ch chan<- Event, pending map[int]*streamToolCall) {
	if len(pending) == 0 {
		return
	}
	idx := make([]int, 0, len(pending))
	for i := range pending {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		call := pending[i]
		if call == nil || call.Name == "" {
			continue
		}
		toolCall := ToolCall{ID: call.ID, Name: call.Name, Args: normalizeArgs([]byte(strings.TrimSpace(call.Args.String())))}
		ch <- Event{Type: EventToolCall, ToolCall: &toolCall}
	}
	for k := range pending {
		delete(pending, k)
	}
}

func parseChatResponse(data []byte) (Response, error) {
	var raw struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Response{}, fmt.Errorf("parse chat response: %w", err)
	}
	if len(raw.Choices) == 0 {
		return Response{}, fmt.Errorf("chat response contained no choices")
	}
	msg := raw.Choices[0].Message
	resp := Response{
		Content: msg.Content,
		Usage: Usage{
			InputTokens:  raw.Usage.PromptTokens,
			OutputTokens: raw.Usage.CompletionTokens,
		},
	}
	for _, tc := range msg.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: normalizeArgs(tc.Function.Arguments),
		})
	}
	if len(resp.ToolCalls) == 0 {
		content, calls, ok := parseContentEnvelope(strings.TrimSpace(resp.Content))
		if ok {
			resp.Content = content
			resp.ToolCalls = calls
		}
	}
	return resp, nil
}

func parseContentEnvelope(content string) (string, []ToolCall, bool) {
	if content == "" || (content[0] != '{' && content[0] != '[') {
		return "", nil, false
	}
	var single map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &single) == nil {
		if rawFinal, ok := single["final"]; ok {
			var final string
			if json.Unmarshal(rawFinal, &final) == nil {
				return final, nil, true
			}
		}
		if rawAction, ok := single["action"]; ok {
			var action string
			if json.Unmarshal(rawAction, &action) == nil && action == "final" {
				var final string
				if json.Unmarshal(single["final"], &final) == nil {
					return final, nil, true
				}
			}
		}
		if rawCalls, ok := single["tool_calls"]; ok {
			calls, err := decodeToolCalls(rawCalls)
			if err == nil && len(calls) > 0 {
				return "", calls, true
			}
		}
		if call, ok := decodeSingleToolCall(single); ok {
			return "", []ToolCall{call}, true
		}
	}
	var many []map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &many) == nil {
		calls := make([]ToolCall, 0, len(many))
		for _, item := range many {
			call, ok := decodeSingleToolCall(item)
			if !ok {
				return "", nil, false
			}
			calls = append(calls, call)
		}
		if len(calls) > 0 {
			return "", calls, true
		}
	}
	return "", nil, false
}

func decodeToolCalls(raw json.RawMessage) ([]ToolCall, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	calls := make([]ToolCall, 0, len(items))
	for _, item := range items {
		call, ok := decodeSingleToolCall(item)
		if !ok {
			return nil, fmt.Errorf("invalid tool call")
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func decodeSingleToolCall(raw map[string]json.RawMessage) (ToolCall, bool) {
	var name string
	for _, key := range []string{"name", "tool", "action"} {
		if data, ok := raw[key]; ok {
			if json.Unmarshal(data, &name) == nil && name != "" && name != "final" {
				break
			}
		}
	}
	if name == "" {
		return ToolCall{}, false
	}
	args := json.RawMessage([]byte(`{}`))
	for _, key := range []string{"args", "arguments"} {
		if data, ok := raw[key]; ok {
			args = normalizeArgs(data)
			break
		}
	}
	var id string
	if data, ok := raw["id"]; ok {
		_ = json.Unmarshal(data, &id)
	}
	return ToolCall{ID: id, Name: name, Args: args}, true
}

func normalizeArgs(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage([]byte(`{}`))
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			trimmed = bytes.TrimSpace([]byte(s))
		}
	}
	if len(trimmed) == 0 {
		return json.RawMessage([]byte(`{}`))
	}
	if json.Valid(trimmed) {
		return json.RawMessage(append([]byte(nil), trimmed...))
	}
	quoted, _ := json.Marshal(string(trimmed))
	return json.RawMessage(quoted)
}

func parseAPIError(status int, data []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		return fmt.Errorf("api error %d: %s", status, payload.Error.Message)
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("api error %d: %s", status, msg)
}

func normalizeBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if baseURL == "" {
		return ""
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}
