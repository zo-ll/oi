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
