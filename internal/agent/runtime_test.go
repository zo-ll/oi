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
	deadlines []deadlineRecord
}

type deadlineRecord struct {
	deadline time.Time
	ok       bool
}

func (f *fakeProvider) Name() string                                         { return f.name }
func (f *fakeProvider) Model() string                                        { return f.model }
func (f *fakeProvider) SetModel(model string)                                { f.model = model }
func (f *fakeProvider) ListModels(context.Context) ([]provider.Model, error) { return nil, nil }
func (f *fakeProvider) ChatStream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, nil
}
func (f *fakeProvider) Chat(ctx context.Context, req provider.Request) (provider.Response, error) {
	f.requests = append(f.requests, req)
	deadline, ok := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadlineRecord{deadline: deadline, ok: ok})
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

type deadlineTool struct {
	deadline time.Time
	ok       bool
}

func (d *deadlineTool) Name() string { return "wait" }
func (d *deadlineTool) Spec() tool.Spec {
	return tool.Spec{Name: "wait", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (d *deadlineTool) Run(ctx context.Context, _ tool.Call) tool.Result {
	d.deadline, d.ok = ctx.Deadline()
	time.Sleep(20 * time.Millisecond)
	return tool.Result{Tool: "wait", OK: true, Output: "waited"}
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

func TestRunOncePersistsAssistantToolCallContentAndIDs(t *testing.T) {
	args := json.RawMessage(`{"text":"tool output"}`)
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{
		{Content: "I'll check.", ToolCalls: []provider.ToolCall{{Name: "echo", Args: args}}},
		{Content: "all done"},
	}}
	r := &Runtime{
		Provider:    p,
		Tools:       tool.NewRegistry(fakeTool{}),
		Policy:      workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		MaxSteps:    3,
		ToolTimeout: time.Second,
	}
	if _, err := r.RunOnce(context.Background(), "use a tool"); err != nil {
		t.Fatal(err)
	}
	if len(r.Session.Messages) < 2 {
		t.Fatalf("session messages = %d", len(r.Session.Messages))
	}
	toolMsg := r.Session.Messages[1]
	if toolMsg.Content != "I'll check." {
		t.Fatalf("stored assistant content = %q", toolMsg.Content)
	}
	if len(toolMsg.ToolCalls) != 1 || toolMsg.ToolCalls[0].ID == "" {
		t.Fatalf("stored tool calls = %+v", toolMsg.ToolCalls)
	}
	replayed := p.requests[1].Messages[2]
	if replayed.Content != "I'll check." || len(replayed.ToolCalls) != 1 || replayed.ToolCalls[0].ID == "" {
		t.Fatalf("replayed assistant message = %+v", replayed)
	}
}

func TestRunOnceMaxStepsExceeded(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{ToolCalls: []provider.ToolCall{{ID: "1", Name: "echo", Args: json.RawMessage(`{"text":"x"}`)}}}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(fakeTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 1, ToolTimeout: time.Second}
	if _, err := r.RunOnce(context.Background(), "loop"); err == nil {
		t.Fatal("expected max steps error")
	}
}

func TestRunOnceAppliesIndependentProviderAndToolTimeouts(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "1", Name: "wait", Args: json.RawMessage(`{}`)}}},
		{Content: "done"},
	}}
	wait := &deadlineTool{}
	r := &Runtime{
		Provider:       p,
		Tools:          tool.NewRegistry(wait),
		Policy:         workspace.Policy{Root: "."},
		MaxSteps:       2,
		ToolTimeout:    100 * time.Millisecond,
		RequestTimeout: 100 * time.Millisecond,
	}
	out, err := r.RunOnce(context.Background(), "use a tool")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	if len(p.deadlines) != 2 {
		t.Fatalf("provider calls = %d", len(p.deadlines))
	}
	for i, d := range p.deadlines {
		if !d.ok {
			t.Fatalf("provider call %d did not receive a deadline", i+1)
		}
	}
	if !wait.ok {
		t.Fatal("tool did not receive a deadline")
	}
	if !p.deadlines[1].deadline.After(p.deadlines[0].deadline) {
		t.Fatalf("second provider deadline was not refreshed: first=%s second=%s", p.deadlines[0].deadline, p.deadlines[1].deadline)
	}
}
