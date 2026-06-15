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

	"github.com/zo-ll/oi/internal/config"
)

var openCodeChatCompletionModels = map[string]Model{
	"big-pickle":             {ID: "big-pickle", Name: "Big Pickle"},
	"deepseek-v4-flash":      {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"},
	"deepseek-v4-flash-free": {ID: "deepseek-v4-flash-free", Name: "DeepSeek V4 Flash Free"},
	"deepseek-v4-pro":        {ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
	"glm-5":                  {ID: "glm-5", Name: "GLM 5"},
	"glm-5.1":                {ID: "glm-5.1", Name: "GLM 5.1"},
	"grok-build-0.1":         {ID: "grok-build-0.1", Name: "Grok Build 0.1"},
	"kimi-k2.5":              {ID: "kimi-k2.5", Name: "Kimi K2.5"},
	"kimi-k2.6":              {ID: "kimi-k2.6", Name: "Kimi K2.6"},
	"kimi-k2.7":              {ID: "kimi-k2.7", Name: "Kimi K2.7"},
	"mimo-v2.5":              {ID: "mimo-v2.5", Name: "MiMo V2.5"},
	"mimo-v2.5-pro":          {ID: "mimo-v2.5-pro", Name: "MiMo V2.5 Pro"},
}

var openCodeMessagesModels = map[string]Model{
	"minimax-m3":   {ID: "minimax-m3", Name: "MiniMax M3"},
	"minimax-m2.7": {ID: "minimax-m2.7", Name: "MiniMax M2.7"},
	"minimax-m2.5": {ID: "minimax-m2.5", Name: "MiniMax M2.5"},
	"qwen3.7-max":  {ID: "qwen3.7-max", Name: "Qwen 3.7 Max"},
	"qwen3.7-plus": {ID: "qwen3.7-plus", Name: "Qwen 3.7 Plus"},
	"qwen3.6-plus": {ID: "qwen3.6-plus", Name: "Qwen 3.6 Plus"},
}

const openCodeMessagesDefaultMaxTokens = 8192

type OpenCodeProvider struct {
	openai   *OpenAIProvider
	messages *OpenCodeMessagesProvider
	model    string
}

func NewOpenCode(name, baseURL, apiKey, model string) (*OpenCodeProvider, error) {
	openai, err := NewOpenAI(name, baseURL, apiKey, model)
	if err != nil {
		return nil, err
	}
	messages, err := NewOpenCodeMessages(name, baseURL, apiKey, model)
	if err != nil {
		return nil, err
	}
	return &OpenCodeProvider{openai: openai, messages: messages, model: model}, nil
}

func (p *OpenCodeProvider) Name() string { return p.openai.Name() }
func (p *OpenCodeProvider) Model() string {
	return p.model
}
func (p *OpenCodeProvider) SetModel(model string) {
	p.model = model
	p.openai.SetModel(model)
	p.messages.SetModel(model)
}

func (p *OpenCodeProvider) Chat(ctx context.Context, req Request) (Response, error) {
	return p.backend(req.Model).Chat(ctx, req)
}

func (p *OpenCodeProvider) ChatStream(ctx context.Context, req Request) (<-chan Event, error) {
	return p.backend(req.Model).ChatStream(ctx, req)
}

func (p *OpenCodeProvider) ListModels(ctx context.Context) ([]Model, error) {
	models, err := p.openai.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	supported := supportedOpenCodeModels()
	out := make([]Model, 0, len(models))
	for _, model := range models {
		if meta, ok := supported[canonicalOpenCodeModelID(model.ID)]; ok {
			if model.Name != "" {
				meta.Name = model.Name
			}
			if model.ContextWindow > 0 {
				meta.ContextWindow = model.ContextWindow
			}
			meta.SupportsThinking = model.SupportsThinking
			out = append(out, meta)
			continue
		}
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (p *OpenCodeProvider) backend(model string) Provider {
	if model == "" {
		model = p.model
	}
	if _, ok := openCodeMessagesModels[canonicalOpenCodeModelID(model)]; ok {
		return p.messages
	}
	return p.openai
}

func supportedOpenCodeModels() map[string]Model {
	out := make(map[string]Model, len(openCodeChatCompletionModels)+len(openCodeMessagesModels))
	for k, v := range openCodeChatCompletionModels {
		out[k] = v
	}
	for k, v := range openCodeMessagesModels {
		out[k] = v
	}
	return out
}

func canonicalOpenCodeModelID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

type OpenCodeMessagesProvider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewOpenCodeMessages(name, baseURL, apiKey, model string) (*OpenCodeMessagesProvider, error) {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if name == "" {
		name = "opencode-go"
	}
	return &OpenCodeMessagesProvider{
		name:    name,
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(strings.TrimPrefix(apiKey, "Bearer ")),
		model:   model,
		client:  &http.Client{},
	}, nil
}

func (p *OpenCodeMessagesProvider) Name() string  { return p.name }
func (p *OpenCodeMessagesProvider) Model() string { return p.model }
func (p *OpenCodeMessagesProvider) SetModel(model string) {
	p.model = model
}

func (p *OpenCodeMessagesProvider) ListModels(ctx context.Context) ([]Model, error) {
	data, err := p.doJSON(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			ContextWindow       int      `json:"context_window"`
			MaxContext          int      `json:"max_context_window"`
			SupportedParameters []string `json:"supported_parameters"`
			Capabilities        struct {
				Reasoning       bool `json:"reasoning"`
				ReasoningEffort bool `json:"reasoning_effort"`
				Thinking        bool `json:"thinking"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	models := make([]Model, 0, len(resp.Data))
	for _, m := range resp.Data {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = m.ID
		}
		window := m.ContextWindow
		if window == 0 {
			window = m.MaxContext
		}
		models = append(models, Model{ID: m.ID, Name: name, ContextWindow: window, SupportsThinking: modelSupportsThinking(m.SupportedParameters, m.Capabilities.Reasoning || m.Capabilities.ReasoningEffort || m.Capabilities.Thinking)})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (p *OpenCodeMessagesProvider) Chat(ctx context.Context, req Request) (Response, error) {
	body, err := p.buildRequest(req, false)
	if err != nil {
		return Response{}, err
	}
	data, err := p.doJSON(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return Response{}, err
	}
	return parseOpenCodeMessagesResponse(data)
}

func (p *OpenCodeMessagesProvider) ChatStream(ctx context.Context, req Request) (<-chan Event, error) {
	body, err := p.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newRequest(ctx, http.MethodPost, "/messages", bytes.NewReader(data))
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

func (p *OpenCodeMessagesProvider) buildRequest(req Request, stream bool) (map[string]any, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	system, messages, err := toOpenCodeMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":      model,
		"messages":   messages,
		"stream":     stream,
		"max_tokens": openCodeMessagesDefaultMaxTokens,
	}
	if system != "" {
		body["system"] = system
	}
	if len(req.Tools) > 0 {
		body["tools"] = toOpenCodeMessageTools(req.Tools)
	}
	return body, nil
}

func toOpenCodeMessages(messages []Message) (string, []map[string]any, error) {
	var system []string
	var out []map[string]any
	for _, m := range messages {
		switch m.Role {
		case "system":
			text := strings.TrimSpace(m.Content)
			if text != "" {
				system = append(system, text)
			}
		case "tool":
			if m.ToolCallID == "" {
				return "", nil, fmt.Errorf("tool message missing tool_call_id")
			}
			out = append(out, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     m.Content,
				}},
			})
		case "assistant":
			content := make([]map[string]any, 0, 1+len(m.ToolCalls))
			if text := strings.TrimSpace(m.Content); text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			for _, tc := range m.ToolCalls {
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": decodeArgsObject(tc.Args),
				})
			}
			if len(content) == 0 {
				content = append(content, map[string]any{"type": "text", "text": ""})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
		default:
			text := m.Content
			out = append(out, map[string]any{"role": m.Role, "content": []map[string]any{{"type": "text", "text": text}}})
		}
	}
	return strings.Join(system, "\n\n"), out, nil
}

func toOpenCodeMessageTools(specs []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		tool := map[string]any{
			"name":         spec.Name,
			"description":  spec.Description,
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		}
		if len(spec.InputSchema) > 0 {
			var decoded any
			if json.Unmarshal(spec.InputSchema, &decoded) == nil {
				if m, ok := decoded.(map[string]any); ok {
					tool["input_schema"] = m
				}
			}
		}
		out = append(out, tool)
	}
	return out
}

func parseOpenCodeMessagesResponse(data []byte) (Response, error) {
	var raw struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Response{}, fmt.Errorf("parse messages response: %w", err)
	}
	var resp Response
	for _, block := range raw.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "thinking":
			resp.Reasoning += block.Thinking
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Args: normalizeArgs(block.Input)})
		}
	}
	resp.Usage = Usage{InputTokens: raw.Usage.InputTokens, OutputTokens: raw.Usage.OutputTokens}
	if len(resp.ToolCalls) == 0 {
		content, calls, ok := parseContentEnvelope(strings.TrimSpace(resp.Content))
		if ok {
			resp.Content = content
			resp.ToolCalls = calls
		}
	}
	return resp, nil
}

func (p *OpenCodeMessagesProvider) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}
	req, err := p.newRequest(ctx, method, path, payload)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return data, nil
}

func (p *OpenCodeMessagesProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
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
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	return req, nil
}

func (p *OpenCodeMessagesProvider) readStream(body io.ReadCloser, ch chan<- Event) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventName := ""
	var dataLines []string
	toolCalls := map[int]*openCodeStreamToolCall{}
	lastUsage := Usage{}

	handle := func(name string, payload string) bool {
		if strings.TrimSpace(payload) == "" {
			return true
		}
		var raw struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			ContentBlock struct {
				Type     string          `json:"type"`
				ID       string          `json:"id"`
				Name     string          `json:"name"`
				Text     string          `json:"text"`
				Thinking string          `json:"thinking"`
				Input    json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type         string `json:"type"`
				Text         string `json:"text"`
				Thinking     string `json:"thinking"`
				PartialJSON  string `json:"partial_json"`
				PartialInput string `json:"partial_input"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			ch <- Event{Err: fmt.Errorf("parse messages stream chunk: %w", err)}
			return false
		}
		kind := name
		if kind == "" {
			kind = raw.Type
		}
		switch kind {
		case "message_start", "message_delta":
			if raw.Usage.InputTokens > 0 {
				lastUsage.InputTokens = raw.Usage.InputTokens
			}
			if raw.Usage.OutputTokens > 0 {
				lastUsage.OutputTokens = raw.Usage.OutputTokens
			}
			if lastUsage.InputTokens > 0 || lastUsage.OutputTokens > 0 {
				ch <- Event{Usage: lastUsage}
			}
		case "content_block_start":
			switch raw.ContentBlock.Type {
			case "text":
				if raw.ContentBlock.Text != "" {
					ch <- Event{Type: EventDelta, Delta: raw.ContentBlock.Text}
				}
			case "thinking":
				if raw.ContentBlock.Thinking != "" {
					ch <- Event{Type: EventDelta, Reasoning: raw.ContentBlock.Thinking}
				}
			case "tool_use":
				toolCalls[raw.Index] = &openCodeStreamToolCall{ID: raw.ContentBlock.ID, Name: raw.ContentBlock.Name, Args: bytes.NewBuffer(normalizeArgs(raw.ContentBlock.Input))}
			}
		case "content_block_delta":
			switch raw.Delta.Type {
			case "text_delta":
				if raw.Delta.Text != "" {
					ch <- Event{Type: EventDelta, Delta: raw.Delta.Text}
				}
			case "thinking_delta":
				if raw.Delta.Thinking != "" {
					ch <- Event{Type: EventDelta, Reasoning: raw.Delta.Thinking}
				}
			case "input_json_delta":
				if call := toolCalls[raw.Index]; call != nil {
					call.append(raw.Delta.PartialJSON)
				}
			case "input_delta":
				if call := toolCalls[raw.Index]; call != nil {
					call.append(raw.Delta.PartialInput)
				}
			}
		case "content_block_stop":
			if call := toolCalls[raw.Index]; call != nil && call.Name != "" {
				ch <- Event{Type: EventToolCall, ToolCall: &ToolCall{ID: call.ID, Name: call.Name, Args: call.args()}}
				delete(toolCalls, raw.Index)
			}
		case "message_stop":
			ch <- Event{Type: EventDone, Done: true}
			return false
		}
		return true
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(dataLines) > 0 {
				if !handle(eventName, strings.Join(dataLines, "\n")) {
					return
				}
			}
			eventName = ""
			dataLines = nil
			continue
		}
		if strings.HasPrefix(trimmed, ":") {
			continue
		}
		if strings.HasPrefix(trimmed, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			continue
		}
		if strings.HasPrefix(trimmed, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- Event{Err: err}
		return
	}
	if len(dataLines) > 0 {
		_ = handle(eventName, strings.Join(dataLines, "\n"))
		return
	}
	ch <- Event{Type: EventDone, Done: true}
}

type openCodeStreamToolCall struct {
	ID   string
	Name string
	Args *bytes.Buffer
}

func (c *openCodeStreamToolCall) append(fragment string) {
	if c == nil || fragment == "" {
		return
	}
	if c.Args == nil {
		c.Args = &bytes.Buffer{}
	}
	if c.Args.Len() == 0 || string(bytes.TrimSpace(c.Args.Bytes())) == "{}" {
		c.Args.Reset()
	}
	c.Args.WriteString(fragment)
}

func (c *openCodeStreamToolCall) args() json.RawMessage {
	if c == nil || c.Args == nil {
		return json.RawMessage([]byte(`{}`))
	}
	return normalizeArgs(c.Args.Bytes())
}

func decodeArgsObject(raw json.RawMessage) any {
	args := normalizeArgs(raw)
	var v any
	if json.Unmarshal(args, &v) == nil {
		return v
	}
	return map[string]any{}
}

// NewForSelection constructs the correct provider for a resolved selection.
func NewForSelection(sel config.Selection) (Provider, error) {
	if sel.Provider == "" {
		return nil, fmt.Errorf("no provider selected")
	}
	switch sel.Provider {
	case "openai-codex":
		return NewOpenAICodex(sel.Provider, sel.BaseURL, sel.APIKey, sel.AccountID, sel.Model)
	case "opencode-go":
		if sel.APIKey == "" {
			return nil, fmt.Errorf("no API key resolved for provider %q", sel.Provider)
		}
		return NewOpenCode(sel.Provider, sel.BaseURL, sel.APIKey, sel.Model)
	default:
		if sel.APIKey == "" {
			return nil, fmt.Errorf("no API key resolved for provider %q", sel.Provider)
		}
		return NewOpenAI(sel.Provider, sel.BaseURL, sel.APIKey, sel.Model)
	}
}
