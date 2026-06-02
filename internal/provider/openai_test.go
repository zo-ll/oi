package provider

import (
	"context"
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
	stream, err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{}
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			usage = ev.Usage
		}
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
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
