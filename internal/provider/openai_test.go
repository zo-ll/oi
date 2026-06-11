package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBaseURL(t *testing.T) {
	if got := normalizeBaseURL("https://example.com"); got != "https://example.com/v1" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeBaseURL("https://example.com/v1/"); got != "https://example.com/v1" {
		t.Fatalf("got %q", got)
	}
}

func TestParseChatResponseNativeToolCall(t *testing.T) {
	data := []byte(`{
		"choices": [{
			"message": {
				"content": "",
				"reasoning_content": "need file contents",
				"tool_calls": [{
					"id": "call_1",
					"type": "function",
					"function": {
						"name": "read_file",
						"arguments": "{\"path\":\"README.md\"}"
					}
				}]
			}
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5}
	}`)
	resp, err := parseChatResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "need file contents" {
		t.Fatalf("reasoning = %q", resp.Reasoning)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool name = %q", resp.ToolCalls[0].Name)
	}
	if string(resp.ToolCalls[0].Args) != `{"path":"README.md"}` {
		t.Fatalf("args = %s", resp.ToolCalls[0].Args)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestParseChatResponseFallbackEnvelope(t *testing.T) {
	data := []byte(`{
		"choices": [{
			"message": {
				"content": "{\"tool_calls\":[{\"name\":\"grep\",\"args\":{\"pattern\":\"auth\"}}]}"
			}
		}]
	}`)
	resp, err := parseChatResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "grep" {
		t.Fatalf("tool name = %q", resp.ToolCalls[0].Name)
	}
}

func TestOpenAIProviderListModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"b-model"},{"id":"a-model"}]}`)
	}))
	defer ts.Close()

	p, err := NewOpenAI("demo", ts.URL, "key", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "b-model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestOpenAIProviderChat(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("auth = %q", got)
		}
		if attempts == 1 {
			http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}]}`)
	}))
	defer ts.Close()

	p, err := NewOpenAI("demo", ts.URL, "key", "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q", resp.Content)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
}

func TestOpenAIProviderChatStreamReportsUsage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	p, err := NewOpenAI("demo", ts.URL, "key", "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	requireStreamSnapshot(t, p, Request{Messages: []Message{{Role: "user", Content: "hi"}}}, "hello", 0, Usage{InputTokens: 12, OutputTokens: 3})
}

func TestOpenAIProviderChatStreamWithToolCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"\"}}]}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"README.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	p, err := NewOpenAI("demo", ts.URL, "key", "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var calls []ToolCall
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		switch ev.Type {
		case EventDelta:
			deltas = append(deltas, ev.Delta)
		case EventToolCall:
			calls = append(calls, *ev.ToolCall)
		}
	}
	if strings.Join(deltas, "") != "hello " {
		t.Fatalf("deltas = %q", strings.Join(deltas, ""))
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].Name != "read_file" || string(calls[0].Args) != `{"path":"README.md"}` {
		t.Fatalf("call = %+v", calls[0])
	}
}

func TestOpenCodeProviderListModelsOnlyIncludesAdvertisedSupportedModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"qwen3.7-max"},{"id":"minimax-m2.7"},{"id":"grok-build-0.1"},{"id":"unknown-model"}]}`)
	}))
	defer ts.Close()

	p, err := NewOpenCode("opencode-go", ts.URL, "key", "")
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, model := range models {
		seen[model.ID] = true
		if model.ID == "unknown-model" {
			t.Fatalf("unexpected unsupported model in list: %+v", model)
		}
	}
	for _, want := range []string{"qwen3.7-max", "minimax-m2.7", "grok-build-0.1"} {
		if !seen[want] {
			t.Fatalf("missing %q in %+v", want, models)
		}
	}
	if seen["deepseek-v4-flash"] {
		t.Fatalf("unexpected fallback-only model in %+v", models)
	}
}

func TestOpenCodeProviderDispatchesMessagesModels(t *testing.T) {
	var chatHits, messagesHits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			messagesHits++
			if got := r.Header.Get("x-api-key"); got != "key" {
				t.Fatalf("x-api-key = %q", got)
			}
			var req struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode messages request: %v", err)
			}
			if req.MaxTokens != openCodeMessagesDefaultMaxTokens {
				t.Fatalf("max_tokens = %d", req.MaxTokens)
			}
			switch req.Model {
			case "minimax-m3":
				fmt.Fprint(w, `{"content":[{"type":"text","text":"hello from minimax"}]}`)
			case "qwen3.6-plus":
				fmt.Fprint(w, `{"content":[{"type":"text","text":"hello from qwen 3.6"}]}`)
			case "qwen3.7-plus":
				fmt.Fprint(w, `{"content":[{"type":"text","text":"hello from qwen plus"}]}`)
			case "qwen3.7-max":
				fmt.Fprint(w, `{"content":[{"type":"text","text":"hello from qwen max"}]}`)
			default:
				t.Fatalf("messages model = %q", req.Model)
			}
		case "/v1/chat/completions":
			chatHits++
			fmt.Fprint(w, `{"choices":[{"message":{"content":"hello from chat"}}]}`)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	p, err := NewOpenCode("opencode-go", ts.URL, "key", "minimax-m3")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello from minimax" {
		t.Fatalf("content = %q", resp.Content)
	}
	if messagesHits != 1 || chatHits != 0 {
		t.Fatalf("messagesHits=%d chatHits=%d", messagesHits, chatHits)
	}

	for _, tc := range []struct {
		model string
		want  string
	}{
		{model: "qwen3.6-plus", want: "hello from qwen 3.6"},
		{model: "qwen3.7-plus", want: "hello from qwen plus"},
		{model: "qwen3.7-max", want: "hello from qwen max"},
	} {
		p.SetModel(tc.model)
		resp, err = p.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != tc.want {
			t.Fatalf("model %s content = %q", tc.model, resp.Content)
		}
	}
	if messagesHits != 4 || chatHits != 0 {
		t.Fatalf("messagesHits=%d chatHits=%d", messagesHits, chatHits)
	}

	p.SetModel("glm-5")
	resp, err = p.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello from chat" {
		t.Fatalf("content = %q", resp.Content)
	}
	if messagesHits != 4 || chatHits != 1 {
		t.Fatalf("messagesHits=%d chatHits=%d", messagesHits, chatHits)
	}
}

func TestOpenCodeMessagesProviderChatParsesToolCallsAndReasoning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "key" {
			t.Fatalf("x-api-key = %q", got)
		}
		fmt.Fprint(w, `{
			"content": [
				{"type":"thinking","thinking":"need file"},
				{"type":"tool_use","id":"call_1","name":"read_file","input":{"path":"README.md"}}
			],
			"usage": {"input_tokens": 9, "output_tokens": 4}
		}`)
	}))
	defer ts.Close()

	p, err := NewOpenCodeMessages("opencode-go", ts.URL, "key", "minimax-m3")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "need file" {
		t.Fatalf("reasoning = %q", resp.Reasoning)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" || string(resp.ToolCalls[0].Args) != `{"path":"README.md"}` {
		t.Fatalf("tool call = %+v", resp.ToolCalls[0])
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOpenCodeMessagesProviderChatStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"usage\":{\"input_tokens\":12}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello \"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer ts.Close()

	p, err := NewOpenCodeMessages("opencode-go", ts.URL, "key", "minimax-m3")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var reasoning []string
	var calls []ToolCall
	var usage Usage
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Reasoning != "" {
			reasoning = append(reasoning, ev.Reasoning)
		}
		if ev.ToolCall != nil {
			calls = append(calls, *ev.ToolCall)
		}
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			usage = ev.Usage
		}
	}
	if strings.Join(deltas, "") != "hello " {
		t.Fatalf("deltas = %q", strings.Join(deltas, ""))
	}
	if strings.Join(reasoning, "") != "plan" {
		t.Fatalf("reasoning = %q", strings.Join(reasoning, ""))
	}
	if len(calls) != 1 || calls[0].Name != "read_file" || string(calls[0].Args) != `{"path":"README.md"}` {
		t.Fatalf("calls = %+v", calls)
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}
