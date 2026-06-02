package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type fakeProvider struct {
	name      string
	model     string
	responses []provider.Response
	chatFn    func(context.Context, provider.Request) (provider.Response, error)
}

func (f *fakeProvider) Name() string  { return f.name }
func (f *fakeProvider) Model() string { return f.model }
func (f *fakeProvider) SetModel(model string) {
	f.model = model
}
func (f *fakeProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "a"}, {ID: "b"}}, nil
}
func (f *fakeProvider) ChatStream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, nil
}
func (f *fakeProvider) Chat(ctx context.Context, req provider.Request) (provider.Response, error) {
	if f.chatFn != nil {
		return f.chatFn(ctx, req)
	}
	if len(f.responses) == 0 {
		return provider.Response{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestHandlePing(t *testing.T) {
	buf := &syncBuffer{}
	s := &Server{cfg: config.Default(), enc: NewEncoder(buf)}
	if err := s.handle(Request{ID: "1", Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"type":"pong"`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestPromptEmitsCompletionEvents(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakeProvider{name: "fake", model: "m", responses: []provider.Response{{Content: "hello"}}}
	s := &Server{
		cfg:       config.Default(),
		selection: config.Selection{Provider: "fake", Model: "m"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		provider:  p,
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(buf),
	}
	s.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1"`) })
	out := buf.String()
	for _, want := range []string{`"type":"started","id":"1"`, `"type":"assistant_delta","id":"1","delta":"hello"`, `"type":"assistant_done","id":"1","message":"hello"`, `"type":"done","id":"1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
}

func TestAbortCancelsActivePrompt(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakeProvider{name: "fake", model: "m", chatFn: func(ctx context.Context, req provider.Request) (provider.Response, error) {
		<-ctx.Done()
		return provider.Response{}, ctx.Err()
	}}
	s := &Server{
		cfg:       config.Default(),
		selection: config.Selection{Provider: "fake", Model: "m"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		provider:  p,
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(buf),
	}
	s.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", Message: "wait"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "abort"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1"`) })
	out := buf.String()
	if !strings.Contains(out, `"type":"aborted","id":"2"`) {
		t.Fatalf("missing aborted event in %s", out)
	}
	if !strings.Contains(out, `"type":"error","id":"1"`) {
		t.Fatalf("missing prompt error in %s", out)
	}
}

func TestSetModelUpdatesProviderRuntimeAndState(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakeProvider{name: "fake", model: "a"}
	s := &Server{
		cfg:       config.Default(),
		selection: config.Selection{Provider: "fake", Model: "a"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		provider:  p,
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(buf),
	}
	s.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "a", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "set_model", Model: "b"}); err != nil {
		t.Fatal(err)
	}
	if p.model != "b" {
		t.Fatalf("provider model = %q", p.model)
	}
	if s.runtime.Session.Model != "b" {
		t.Fatalf("session model = %q", s.runtime.Session.Model)
	}
	if !strings.Contains(buf.String(), `"type":"state","id":"1"`) || !strings.Contains(buf.String(), `"model":"b"`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestSetProviderResetsRuntimeAndState(t *testing.T) {
	buf := &syncBuffer{}
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{
		"alpha": {BaseURL: "https://example.invalid/v1"},
		"beta":  {BaseURL: "https://example.invalid/v1"},
	}
	s := &Server{
		cfg:       cfg,
		auth:      &config.Auth{Keys: map[string]string{"alpha": "ka", "beta": "kb"}},
		selection: config.Selection{Provider: "alpha", Model: "m1"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(buf),
		newProvider: func(sel config.Selection) (provider.Provider, error) {
			return &fakeProvider{name: sel.Provider, model: sel.Model}, nil
		},
	}
	if err := s.resetRuntime(); err != nil {
		t.Fatal(err)
	}
	s.runtime.Session.Messages = append(s.runtime.Session.Messages, session.Message{Role: "user", Content: "old"})
	if err := s.handle(Request{ID: "1", Type: "set_provider", Provider: "beta"}); err != nil {
		t.Fatal(err)
	}
	if s.selection.Provider != "beta" {
		t.Fatalf("selection = %+v", s.selection)
	}
	if s.runtime.Provider == nil || s.runtime.Provider.Name() != "beta" {
		t.Fatalf("runtime provider = %+v", s.runtime.Provider)
	}
	if s.runtime.Session.Provider != "beta" {
		t.Fatalf("session provider = %q", s.runtime.Session.Provider)
	}
	if len(s.runtime.Session.Messages) != 0 {
		t.Fatalf("session was not reset: %+v", s.runtime.Session.Messages)
	}
	if !strings.Contains(buf.String(), `"type":"state","id":"1"`) || !strings.Contains(buf.String(), `"provider":"beta"`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestBusyBlocksStateChanges(t *testing.T) {
	buf := &syncBuffer{}
	p := &fakeProvider{name: "fake", model: "m", chatFn: func(ctx context.Context, req provider.Request) (provider.Response, error) {
		<-ctx.Done()
		return provider.Response{}, ctx.Err()
	}}
	s := &Server{
		cfg:       config.Default(),
		selection: config.Selection{Provider: "fake", Model: "m"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		provider:  p,
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(buf),
	}
	s.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", Message: "wait"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.isBusy() })
	if err := s.handle(Request{ID: "2", Type: "set_model", Model: "x"}); err == nil || err.Error() != "cannot change model while a request is running" {
		t.Fatalf("set_model err = %v", err)
	}
	if err := s.handle(Request{ID: "3", Type: "set_provider", Provider: "demo"}); err == nil || err.Error() != "cannot change provider while a request is running" {
		t.Fatalf("set_provider err = %v", err)
	}
	if err := s.handle(Request{ID: "4", Type: "new_session"}); err == nil || err.Error() != "cannot reset session while a request is running" {
		t.Fatalf("new_session err = %v", err)
	}
	if err := s.handle(Request{ID: "5", Type: "abort"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1"`) })
}

func TestListModelsWithoutProviderConfigured(t *testing.T) {
	buf := &syncBuffer{}
	s := &Server{
		cfg:         config.Default(),
		auth:        &config.Auth{},
		selection:   config.Selection{},
		policy:      workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		tools:       tool.NewRegistry(),
		enc:         NewEncoder(buf),
		newProvider: func(sel config.Selection) (provider.Provider, error) { return nil, nil },
	}
	if err := s.handle(Request{ID: "1", Type: "list_models"}); err == nil || err.Error() != "no provider configured" {
		t.Fatalf("err = %v", err)
	}
}

func TestServeEmitsReadyAndErrorFrames(t *testing.T) {
	cfg := config.Default()
	s := &Server{cfg: cfg, auth: &config.Auth{}, selection: config.Selection{}, policy: workspace.Policy{Root: "."}, tools: tool.NewRegistry()}
	var in bytes.Buffer
	in.WriteString("{\"id\":\"1\",\"type\":\"unknown\"}\n")
	var out syncBuffer
	if err := s.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, `"type":"ready"`) || !strings.Contains(text, `"type":"error","id":"1","error":"unknown command: unknown"`) {
		t.Fatalf("output = %s", text)
	}
}

func TestStateAndListProviders(t *testing.T) {
	buf := &syncBuffer{}
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"alpha": {BaseURL: "https://example.invalid/v1"}, "beta": {BaseURL: "https://example.invalid/v1"}}
	s := &Server{cfg: cfg, selection: config.Selection{Provider: "alpha", Model: "m"}, policy: workspace.Policy{Root: "."}, enc: NewEncoder(buf)}
	if err := s.handle(Request{ID: "1", Type: "list_providers"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "get_state"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"type":"providers"`) || !strings.Contains(out, `"alpha"`) || !strings.Contains(out, `"beta"`) {
		t.Fatalf("output = %s", out)
	}
	if !strings.Contains(out, `"type":"state"`) || !strings.Contains(out, `"provider":"alpha"`) {
		t.Fatalf("output = %s", out)
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(errors.New("timeout waiting for condition"))
}

func TestEventJSONDataField(t *testing.T) {
	data, err := json.Marshal(Event{Type: "x", Data: map[string]string{"a": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"data":{"a":"b"}`) {
		t.Fatalf("json = %s", string(data))
	}
}
