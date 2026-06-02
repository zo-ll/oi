package session

import (
	"strings"
	"testing"
)

func TestCompactMessagesCreatesSummaryAndKeepsRecentTail(t *testing.T) {
	messages := []Message{
		{Role: "user", Kind: "talk", Content: strings.Repeat("old user ", 40)},
		{Role: "assistant", Kind: "talk", Content: strings.Repeat("old assistant ", 40)},
		{Role: "assistant", Kind: "tool_call", Content: "checking", ToolCalls: []ToolCall{{ID: "1", Name: "read", Args: []byte(`{"path":"a.go"}`)}}},
		{Role: "tool", Kind: "tool_result", ToolCallID: "1", Content: strings.Repeat("{\"ok\":true,\"output\":\"x\"}", 20)},
		{Role: "user", Kind: "talk", Content: "recent user"},
		{Role: "assistant", Kind: "talk", Content: "recent assistant"},
		{Role: "user", Kind: "talk", Content: "latest user"},
		{Role: "assistant", Kind: "talk", Content: "latest assistant"},
	}
	compacted, changed := CompactMessages(messages, 10)
	if !changed {
		t.Fatal("expected compaction")
	}
	if len(compacted) != 5 {
		t.Fatalf("len = %d", len(compacted))
	}
	if compacted[0].Kind != "summary" || compacted[0].Role != "system" {
		t.Fatalf("summary = %+v", compacted[0])
	}
	if !strings.Contains(compacted[0].Content, "assistant called tools") {
		t.Fatalf("summary content = %q", compacted[0].Content)
	}
	if compacted[len(compacted)-1].Content != "latest assistant" {
		t.Fatalf("tail lost: %+v", compacted)
	}
}

func TestCompactMessagesNoopWhenWithinBudget(t *testing.T) {
	messages := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	compacted, changed := CompactMessages(messages, 1000)
	if changed {
		t.Fatal("unexpected compaction")
	}
	if len(compacted) != len(messages) {
		t.Fatalf("len = %d", len(compacted))
	}
}
