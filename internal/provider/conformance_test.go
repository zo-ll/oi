package provider

import (
	"context"
	"testing"
)

type streamSnapshot struct {
	Text  string
	Calls []ToolCall
	Usage Usage
}

func collectStreamSnapshot(t *testing.T, stream <-chan Event) streamSnapshot {
	t.Helper()
	var snap streamSnapshot
	for ev := range stream {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		snap.Text += ev.Delta
		if ev.ToolCall != nil {
			snap.Calls = append(snap.Calls, *ev.ToolCall)
		}
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			snap.Usage = ev.Usage
		}
	}
	return snap
}

func requireStreamSnapshot(t *testing.T, p Provider, req Request, wantText string, wantCalls int, wantUsage Usage) {
	t.Helper()
	stream, err := p.ChatStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	snap := collectStreamSnapshot(t, stream)
	if snap.Text != wantText {
		t.Fatalf("text = %q want %q", snap.Text, wantText)
	}
	if len(snap.Calls) != wantCalls {
		t.Fatalf("calls = %+v", snap.Calls)
	}
	if snap.Usage != wantUsage {
		t.Fatalf("usage = %+v want %+v", snap.Usage, wantUsage)
	}
}
