package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type fakeProvider struct {
	name      string
	model     string
	responses []provider.Response
	requests  []provider.Request
}

func (f *fakeProvider) Name() string                                         { return f.name }
func (f *fakeProvider) Model() string                                        { return f.model }
func (f *fakeProvider) SetModel(model string)                                { f.model = model }
func (f *fakeProvider) ListModels(context.Context) ([]provider.Model, error) { return nil, nil }
func (f *fakeProvider) ChatStream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, nil
}
func (f *fakeProvider) Chat(_ context.Context, req provider.Request) (provider.Response, error) {
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return provider.Response{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type fakeTool struct{}

func (fakeTool) Name() string { return "echo" }
func (fakeTool) Spec() tool.Spec {
	return tool.Spec{Name: "echo", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}
}
func (fakeTool) Run(_ context.Context, call tool.Call) tool.Result {
	var args struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(call.Args, &args)
	return tool.Result{Tool: "echo", OK: true, Output: args.Text}
}

func TestRunOnceFinalAnswer(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "done"}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, MaxSteps: 2}
	out, err := r.RunOnce(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	if len(p.requests) != 1 {
		t.Fatalf("requests = %d", len(p.requests))
	}
}

func TestRunOnceToolThenFinal(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"text": "tool output"})
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{
		{Reasoning: "need a tool", ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "echo", Args: args}}},
		{Content: "all done"},
	}}
	r := &Runtime{
		Provider:    p,
		Tools:       tool.NewRegistry(fakeTool{}),
		Policy:      workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		MaxSteps:    3,
		ToolTimeout: time.Second,
	}
	out, err := r.RunOnce(context.Background(), "use a tool")
	if err != nil {
		t.Fatal(err)
	}
	if out != "all done" {
		t.Fatalf("out = %q", out)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests = %d", len(p.requests))
	}
	last := p.requests[1].Messages
	if len(last) < 4 {
		t.Fatalf("history too short: %d", len(last))
	}
	if last[len(last)-1].Role != "tool" {
		t.Fatalf("last role = %q", last[len(last)-1].Role)
	}
	if last[len(last)-1].ToolCallID != "call_1" {
		t.Fatalf("tool_call_id = %q", last[len(last)-1].ToolCallID)
	}
	if got := p.requests[1].Messages[2].Reasoning; got != "need a tool" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestRunOnceMaxStepsExceeded(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{ToolCalls: []provider.ToolCall{{ID: "1", Name: "echo", Args: json.RawMessage(`{"text":"x"}`)}}}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(fakeTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 1, ToolTimeout: time.Second}
	if _, err := r.RunOnce(context.Background(), "loop"); err == nil {
		t.Fatal("expected max steps error")
	}
}
