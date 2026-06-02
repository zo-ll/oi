package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveCodexURL(t *testing.T) {
	if got := resolveCodexURL("https://chatgpt.com/backend-api"); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("got %q", got)
	}
	if got := resolveCodexURL("https://chatgpt.com/backend-api/codex"); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveCodexModelsURL(t *testing.T) {
	if got := resolveCodexModelsURL("https://chatgpt.com/backend-api"); got != "https://chatgpt.com/backend-api/codex/models?client_version=1.0.0" {
		t.Fatalf("got %q", got)
	}
	if got := resolveCodexModelsURL("https://chatgpt.com/backend-api/codex"); got != "https://chatgpt.com/backend-api/codex/models?client_version=1.0.0" {
		t.Fatalf("got %q", got)
	}
	if got := resolveCodexModelsURL("https://chatgpt.com/backend-api/codex/responses"); got != "https://chatgpt.com/backend-api/codex/models?client_version=1.0.0" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAICodexProviderChatStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("auth = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct" {
			t.Fatalf("account = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"path\\\":\\\"README.md\\\"}\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	p, err := NewOpenAICodex("openai-codex", ts.URL, "tok", "acct", "gpt-5.3-codex")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var calls []ToolCall
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Delta != "" {
			text += ev.Delta
		}
		if ev.ToolCall != nil {
			calls = append(calls, *ev.ToolCall)
		}
	}
	if text != "hello " {
		t.Fatalf("text = %q", text)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" || string(calls[0].Args) != `{"path":"README.md"}` {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestOpenAICodexProviderHandlesDoneTextAndMultipleCalls(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.done\",\"text\":\"fallback text\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_2\",\"call_id\":\"call_2\",\"name\":\"list_files\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_2\",\"delta\":\"{\\\"path\\\":\\\".\\\"}\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"read_file\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"item_2\",\"call_id\":\"call_2\",\"name\":\"list_files\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	p, err := NewOpenAICodex("openai-codex", ts.URL, "tok", "acct", "gpt-5.3-codex")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var calls []ToolCall
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		text += ev.Delta
		if ev.ToolCall != nil {
			calls = append(calls, *ev.ToolCall)
		}
	}
	if text != "fallback text" {
		t.Fatalf("text = %q", text)
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls = %+v", calls)
	}
	if string(calls[0].Args) != `{"path":"README.md"}` || string(calls[1].Args) != `{"path":"."}` {
		t.Fatalf("args = %s / %s", calls[0].Args, calls[1].Args)
	}
}

func TestOpenAICodexProviderListModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != "1.0.0" {
			t.Fatalf("client_version = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("auth = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct" {
			t.Fatalf("account = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":272000},{"slug":"gpt-5.4-mini","display_name":"GPT-5.4-Mini","context_window":128000}]}`)
	}))
	defer ts.Close()

	p, err := NewOpenAICodex("openai-codex", ts.URL, "tok", "acct", "gpt-5.5")
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-5.5" || models[1].ID != "gpt-5.4-mini" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ContextWindow != 272000 || models[1].ContextWindow != 128000 {
		t.Fatalf("models = %+v", models)
	}
}

func TestOpenAICodexProviderChat(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	p, err := NewOpenAICodex("openai-codex", ts.URL, "tok", "acct", "gpt-5.3-codex")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q", resp.Content)
	}
}
