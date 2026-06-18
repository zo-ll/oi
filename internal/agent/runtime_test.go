package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/retrieval"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type fakeProvider struct {
	name            string
	model           string
	responses       []provider.Response
	streamResponses [][]provider.Event
	requests        []provider.Request
	deadlines       []deadlineRecord
}

type deadlineRecord struct {
	deadline time.Time
	ok       bool
}

func (f *fakeProvider) Name() string                                         { return f.name }
func (f *fakeProvider) Model() string                                        { return f.model }
func (f *fakeProvider) SetModel(model string)                                { f.model = model }
func (f *fakeProvider) ListModels(context.Context) ([]provider.Model, error) { return nil, nil }
func (f *fakeProvider) ChatStream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	f.requests = append(f.requests, req)
	deadline, ok := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadlineRecord{deadline: deadline, ok: ok})
	events := []provider.Event{{Delta: "", Done: true}}
	if len(f.streamResponses) > 0 {
		events = f.streamResponses[0]
		f.streamResponses = f.streamResponses[1:]
	}
	ch := make(chan provider.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch, nil
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

type failingTool struct{}

func (d *deadlineTool) Name() string { return "wait" }
func (d *deadlineTool) Spec() tool.Spec {
	return tool.Spec{Name: "wait", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (d *deadlineTool) Run(ctx context.Context, _ tool.Call) tool.Result {
	d.deadline, d.ok = ctx.Deadline()
	time.Sleep(20 * time.Millisecond)
	return tool.Result{Tool: "wait", OK: true, Output: "waited"}
}

func (failingTool) Name() string { return "fail" }
func (failingTool) Spec() tool.Spec {
	return tool.Spec{Name: "fail", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (failingTool) Run(_ context.Context, _ tool.Call) tool.Result {
	return tool.Result{Tool: "fail", OK: false, Error: "boom"}
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

func TestRunOnceDrainsSteeringBeforeNextModelCall(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "first"}, {Content: "steered"}}}
	drained := false
	r := &Runtime{
		Provider: p,
		Tools:    tool.NewRegistry(),
		Policy:   workspace.Policy{Root: "."},
		MaxSteps: 3,
		DrainSteering: func() []string {
			if drained {
				return nil
			}
			drained = true
			return []string{"change course"}
		},
	}
	out, err := r.RunOnce(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "steered" {
		t.Fatalf("out = %q", out)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests = %d", len(p.requests))
	}
	last := p.requests[1].Messages[len(p.requests[1].Messages)-1]
	if last.Role != "user" || last.Content != "change course" {
		t.Fatalf("last message = %+v", last)
	}
}

func TestMaybeAutoCompactTriggersAtThreshold(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m"}
	r := &Runtime{
		Provider:             p,
		Tools:                tool.NewRegistry(),
		Policy:               workspace.Policy{Root: "."},
		Session:              session.New("fake", "m", "."),
		ContextWindow:        100000,
		AutoCompactThreshold: 90,
		LastUsage:            provider.Usage{InputTokens: 95000},
	}
	r.Session.Messages = append(r.Session.Messages, session.Message{Role: "user", Content: strings.Repeat("x", 5000)})
	r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: strings.Repeat("y", 5000)})
	for i := 0; i < 6; i++ {
		r.Session.Messages = append(r.Session.Messages, session.Message{Role: "user", Content: "more " + strings.Repeat("z", 800)})
		r.Session.Messages = append(r.Session.Messages, session.Message{Role: "assistant", Content: "ok " + strings.Repeat("w", 800)})
	}
	if !r.maybeAutoCompact() {
		t.Fatal("expected auto-compact to trigger")
	}
	if len(r.Session.Messages) == 0 || r.Session.Messages[0].Kind != "summary" {
		t.Fatalf("expected compacted summary at head: %+v", r.Session.Messages)
	}
	if session.EstimateTokens(r.Session.Messages) >= 95000 {
		t.Fatalf("estimated tokens not reduced: %d", session.EstimateTokens(r.Session.Messages))
	}
}

func TestMaybeAutoCompactSkipsBelowThreshold(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m"}
	r := &Runtime{
		Provider:             p,
		Tools:                tool.NewRegistry(),
		Policy:               workspace.Policy{Root: "."},
		Session:              session.New("fake", "m", "."),
		ContextWindow:        100000,
		AutoCompactThreshold: 90,
		LastUsage:            provider.Usage{InputTokens: 1000},
	}
	if r.maybeAutoCompact() {
		t.Fatal("expected no auto-compact below threshold")
	}
}

func TestRunOnceStreamSuppressesContentFromToolPlanningStep(t *testing.T) {
	callArgs := json.RawMessage(`{"text":"ok"}`)
	p := &fakeProvider{name: "fake", model: "m", streamResponses: [][]provider.Event{
		{{Reasoning: "need tool"}, {Delta: "first."}, {ToolCall: &provider.ToolCall{ID: "1", Name: "echo", Args: callArgs}}, {Done: true}},
		{{Delta: "final"}, {Done: true}},
	}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(fakeTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 3}
	var deltas []string
	out, err := r.RunOnceStream(context.Background(), "hello", func(delta string, reasoning bool) {
		if reasoning {
			deltas = append(deltas, "reasoning:"+delta)
			return
		}
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "final" {
		t.Fatalf("out = %q", out)
	}
	got := strings.Join(deltas, "|")
	if got != "reasoning:need tool|final" {
		t.Fatalf("deltas = %q", got)
	}
}

func TestRunOnceStreamObservedStreamsDraftAndMarksToolStep(t *testing.T) {
	callArgs := json.RawMessage(`{"text":"ok"}`)
	p := &fakeProvider{name: "fake", model: "m", streamResponses: [][]provider.Event{
		{{Delta: "draft"}, {ToolCall: &provider.ToolCall{ID: "1", Name: "echo", Args: callArgs}}, {Done: true}},
		{{Delta: "final"}, {Done: true}},
	}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(fakeTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 3}
	var deltas []string
	var steps []bool
	out, err := r.RunOnceStreamObserved(context.Background(), "hello", StreamObserver{
		Delta: func(delta string, reasoning bool) {
			if !reasoning {
				deltas = append(deltas, delta)
			}
		},
		StepDone: func(toolCalls bool) {
			steps = append(steps, toolCalls)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "final" {
		t.Fatalf("out = %q", out)
	}
	if got := strings.Join(deltas, "|"); got != "draft|final" {
		t.Fatalf("deltas = %q", got)
	}
	if len(steps) != 2 || !steps[0] || steps[1] {
		t.Fatalf("steps = %#v", steps)
	}
}

func TestRunOnceStreamFlushesFinalContentAfterStepIsFinal(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", streamResponses: [][]provider.Event{
		{{Delta: strings.Repeat("a", 48)}, {Delta: "b"}, {Done: true}},
	}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, MaxSteps: 1}
	var deltas []string
	out, err := r.RunOnceStream(context.Background(), "hello", func(delta string, reasoning bool) {
		if !reasoning {
			deltas = append(deltas, delta)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != strings.Repeat("a", 48)+"b" {
		t.Fatalf("out = %q", out)
	}
	if len(deltas) != 1 || deltas[0] != strings.Repeat("a", 48)+"b" {
		t.Fatalf("deltas = %#v", deltas)
	}
}

func TestRunOnceInjectsRetrievalContextForCodeQuestions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "runtime.go"), []byte("package demo\n\nfunc RunOnce() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "done"}}}
	var notice retrieval.Notice
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: root}, MaxSteps: 2, OnRetrieve: func(n retrieval.Notice) { notice = n }}
	if _, err := r.RunOnce(context.Background(), "where is RunOnce implemented in runtime.go"); err != nil {
		t.Fatal(err)
	}
	if notice.SnippetCount == 0 {
		t.Fatalf("notice = %+v", notice)
	}
	if len(p.requests) != 1 || len(p.requests[0].Messages) < 3 {
		t.Fatalf("requests = %+v", p.requests)
	}
	if p.requests[0].Messages[1].Role != "system" || !strings.Contains(p.requests[0].Messages[1].Content, "Retrieved workspace context:") {
		t.Fatalf("messages = %+v", p.requests[0].Messages)
	}
	if !strings.Contains(p.requests[0].Messages[1].Content, "runtime.go") {
		t.Fatalf("retrieval context = %q", p.requests[0].Messages[1].Content)
	}
}

func TestRunOncePrependsSystemPromptEveryTurn(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "one"}, {Content: "two"}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, MaxSteps: 2}
	if _, err := r.RunOnce(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests = %d", len(p.requests))
	}
	for i, req := range p.requests {
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Fatalf("request %d messages = %+v", i+1, req.Messages)
		}
	}
}

func TestCompactSessionCompactsLargeSession(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m"}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, ContextWindow: 100}
	r.Session = session.New("fake", "m", ".")
	for i := 0; i < 10; i++ {
		r.Session.Messages = append(r.Session.Messages,
			session.Message{Role: "user", Kind: "talk", Content: strings.Repeat("old user text ", 20)},
			session.Message{Role: "assistant", Kind: "talk", Content: strings.Repeat("old assistant text ", 20)},
		)
	}
	changed, _ := r.ForceCompactSession()
	if !changed {
		t.Fatal("expected compaction")
	}
	if len(r.Session.Messages) == 0 || r.Session.Messages[0].Kind != "summary" {
		t.Fatalf("session messages = %+v", r.Session.Messages)
	}
}

func TestForceCompactSessionCompactsSingleMessage(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m"}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, ContextWindow: 100}
	r.Session = session.New("fake", "m", ".")
	r.Session.Messages = append(r.Session.Messages, session.Message{Role: "user", Kind: "talk", Content: "hello there"})
	changed, _ := r.ForceCompactSession()
	if !changed {
		t.Fatal("expected compaction")
	}
	if len(r.Session.Messages) != 1 || r.Session.Messages[0].Kind != "summary" {
		t.Fatalf("session messages = %+v", r.Session.Messages)
	}
}

func TestRunOnceSkipsRetrievalForChitChat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "runtime.go"), []byte("package demo\n\nfunc RunOnce() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "done"}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: root}, MaxSteps: 2}
	for _, input := range []string{"hello there", "what do you think of this project?"} {
		p.requests = nil
		p.responses = []provider.Response{{Content: "done"}}
		if _, err := r.RunOnce(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if len(p.requests) != 1 || len(p.requests[0].Messages) < 2 {
			t.Fatalf("requests = %+v", p.requests)
		}
		for _, msg := range p.requests[0].Messages {
			if msg.Role == "system" && strings.Contains(msg.Content, "Retrieved workspace context:") {
				t.Fatalf("unexpected retrieval context for %q: %+v", input, p.requests[0].Messages)
			}
		}
	}
}

func TestRunOnceDoesNotCompactAutomatically(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "done"}}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(), Policy: workspace.Policy{Root: "."}, MaxSteps: 2, ContextWindow: 100, AutoCompactThreshold: -1}
	r.Session = session.New("fake", "m", ".")
	for i := 0; i < 10; i++ {
		r.Session.Messages = append(r.Session.Messages,
			session.Message{Role: "user", Kind: "talk", Content: strings.Repeat("old user text ", 20)},
			session.Message{Role: "assistant", Kind: "talk", Content: strings.Repeat("old assistant text ", 20)},
		)
	}
	if _, err := r.RunOnce(context.Background(), "latest"); err != nil {
		t.Fatal(err)
	}
	if len(r.Session.Messages) > 0 && r.Session.Messages[0].Kind == "summary" {
		t.Fatalf("session compacted unexpectedly: %+v", r.Session.Messages[0])
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

func TestRunOnceStopsRepeatedIdenticalToolCalls(t *testing.T) {
	args := json.RawMessage(`{"text":"x"}`)
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{Name: "echo", Args: args}}},
		{ToolCalls: []provider.ToolCall{{Name: "echo", Args: args}}},
		{ToolCalls: []provider.ToolCall{{Name: "echo", Args: args}}},
	}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(fakeTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 10, ToolTimeout: time.Second}
	if _, err := r.RunOnce(context.Background(), "loop"); err == nil || err.Error() != "stalled: repeated identical tool calls" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOnceStopsRepeatedIdenticalToolErrors(t *testing.T) {
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{
		{ToolCalls: []provider.ToolCall{{Name: "fail", Args: json.RawMessage(`{}`)}}},
		{ToolCalls: []provider.ToolCall{{Name: "fail", Args: json.RawMessage(`{}`)}}},
	}}
	r := &Runtime{Provider: p, Tools: tool.NewRegistry(failingTool{}), Policy: workspace.Policy{Root: "."}, MaxSteps: 10, ToolTimeout: time.Second}
	if _, err := r.RunOnce(context.Background(), "loop"); err == nil || err.Error() != "stalled: repeated identical tool error" {
		t.Fatalf("err = %v", err)
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
