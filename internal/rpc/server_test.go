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
	name            string
	model           string
	responses       []provider.Response
	streamResponses [][]provider.Event
	streamFn        func(context.Context, provider.Request) (<-chan provider.Event, error)
	chatFn          func(context.Context, provider.Request) (provider.Response, error)
}

func (f *fakeProvider) Name() string  { return f.name }
func (f *fakeProvider) Model() string { return f.model }
func (f *fakeProvider) SetModel(model string) {
	f.model = model
}
func (f *fakeProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "a"}, {ID: "b"}}, nil
}
func (f *fakeProvider) ChatStream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if f.streamFn != nil {
		return f.streamFn(ctx, req)
	}
	events := []provider.Event{{Done: true}}
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

func newTestServer() *Server {
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"fake": {BaseURL: "https://example.invalid/v1"}}
	s := &Server{
		cfg:       cfg,
		auth:      &config.Auth{Keys: map[string]string{"fake": "k"}},
		selection: config.Selection{Provider: "fake", Model: "m"},
		policy:    workspace.Policy{Root: ".", ApprovalMode: workspace.ApprovalAuto},
		sessions:  map[string]*rpcSession{},
		tools:     tool.NewRegistry(),
		enc:       NewEncoder(&syncBuffer{}),
		newProvider: func(sel config.Selection) (provider.Provider, error) {
			return &fakeProvider{name: valueOr(sel.Provider, "fake"), model: valueOr(sel.Model, "m")}, nil
		},
	}
	rpcSess, _ := s.newRPCSession("s1", s.selection)
	s.sessions[rpcSess.id] = rpcSess
	s.activeSessionID = rpcSess.id
	return s
}

func TestHandlePing(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	if err := s.handle(Request{ID: "1", Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"type":"pong"`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestCreateUseListCloseSessions(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	if err := s.handle(Request{ID: "1", Type: "create_session", SessionID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "list_sessions"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "3", Type: "use_session", SessionID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "4", Type: "close_session", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"session_id":"work"`) || !strings.Contains(out, `"active_session_id":"work"`) || !strings.Contains(out, `"type":"sessions"`) {
		t.Fatalf("output = %s", out)
	}
	if _, err := s.sessionByID("s1"); err == nil {
		t.Fatal("expected s1 to be closed")
	}
}

func TestCreateSessionRejectsInvalidOrDuplicateIDs(t *testing.T) {
	s := newTestServer()
	if err := s.handle(Request{ID: "1", Type: "create_session", SessionID: "bad/id"}); err == nil || err.Error() != "invalid session_id" {
		t.Fatalf("invalid id err = %v", err)
	}
	if err := s.handle(Request{ID: "2", Type: "create_session", SessionID: "s1"}); err == nil || err.Error() != "session already exists: s1" {
		t.Fatalf("duplicate id err = %v", err)
	}
}

func TestPromptEmitsCompletionEvents(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	p := &fakeProvider{name: "fake", model: "m", streamResponses: [][]provider.Event{{
		{Delta: "hel"},
		{Delta: "lo"},
		{Done: true},
	}}}
	rpcSess, _ := s.sessionByID("s1")
	rpcSess.provider = p
	rpcSess.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", SessionID: "s1", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1","session_id":"s1"`) })
	out := buf.String()
	for _, want := range []string{`"type":"started","id":"1","session_id":"s1"`, `"type":"assistant_delta","id":"1","session_id":"s1","delta":"hel"`, `"type":"assistant_delta","id":"1","session_id":"s1","delta":"lo"`, `"type":"assistant_done","id":"1","session_id":"s1","message":"hello"`, `"type":"done","id":"1","session_id":"s1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
}

func TestAbortCancelsActivePromptInTargetSession(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	p := &fakeProvider{name: "fake", model: "m", streamFn: func(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
		ch := make(chan provider.Event)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}}
	rpcSess, _ := s.sessionByID("s1")
	rpcSess.provider = p
	rpcSess.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", SessionID: "s1", Message: "wait"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "abort", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1","session_id":"s1"`) })
	out := buf.String()
	if !strings.Contains(out, `"type":"aborted","id":"2","session_id":"s1"`) {
		t.Fatalf("missing aborted event in %s", out)
	}
	if !strings.Contains(out, `"type":"error","id":"1","session_id":"s1"`) {
		t.Fatalf("missing prompt error in %s", out)
	}
}

func TestSetModelUpdatesTargetSessionAndState(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	rpcSess, _ := s.sessionByID("s1")
	p := &fakeProvider{name: "fake", model: "a"}
	rpcSess.provider = p
	rpcSess.runtime = &agent.Runtime{Provider: p, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "a", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "set_model", SessionID: "s1", Model: "b"}); err != nil {
		t.Fatal(err)
	}
	if p.model != "b" {
		t.Fatalf("provider model = %q", p.model)
	}
	if rpcSess.runtime.Session.Model != "b" {
		t.Fatalf("session model = %q", rpcSess.runtime.Session.Model)
	}
	if !strings.Contains(buf.String(), `"type":"state","id":"1","session_id":"s1"`) || !strings.Contains(buf.String(), `"model":"b"`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestBusyChecksArePerTargetSession(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	if err := s.handle(Request{ID: "c", Type: "create_session", SessionID: "s2"}); err != nil {
		t.Fatal(err)
	}
	s1, _ := s.sessionByID("s1")
	s2, _ := s.sessionByID("s2")
	p1 := &fakeProvider{name: "fake", model: "m", streamFn: func(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
		ch := make(chan provider.Event)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}}
	p2 := &fakeProvider{name: "fake", model: "m"}
	s1.provider = p1
	s1.runtime = &agent.Runtime{Provider: p1, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	s2.provider = p2
	s2.runtime = &agent.Runtime{Provider: p2, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", SessionID: "s1", Message: "wait"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "set_model", SessionID: "s1", Model: "x"}); err == nil || err.Error() != "cannot change model while a request is running" {
		t.Fatalf("set_model busy err = %v", err)
	}
	if err := s.handle(Request{ID: "3", Type: "new_session", SessionID: "s1"}); err == nil || err.Error() != "cannot reset session while a request is running" {
		t.Fatalf("new_session busy err = %v", err)
	}
	if err := s.handle(Request{ID: "4", Type: "close_session", SessionID: "s1"}); err == nil || err.Error() != "cannot close session while a request is running" {
		t.Fatalf("close_session busy err = %v", err)
	}
	if err := s.handle(Request{ID: "5", Type: "set_model", SessionID: "s2", Model: "ok"}); err != nil {
		t.Fatalf("other session should stay mutable: %v", err)
	}
	if err := s.handle(Request{ID: "6", Type: "abort", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1","session_id":"s1"`) })
}

func TestConcurrentPromptsAcrossSessions(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	if err := s.handle(Request{ID: "c", Type: "create_session", SessionID: "s2"}); err != nil {
		t.Fatal(err)
	}
	s1, _ := s.sessionByID("s1")
	s2, _ := s.sessionByID("s2")
	p1 := &fakeProvider{name: "fake", model: "m", streamFn: func(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
		ch := make(chan provider.Event)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	}}
	p2 := &fakeProvider{name: "fake", model: "m", streamResponses: [][]provider.Event{{{Delta: "ok"}, {Done: true}}}}
	s1.provider = p1
	s1.runtime = &agent.Runtime{Provider: p1, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	s2.provider = p2
	s2.runtime = &agent.Runtime{Provider: p2, Tools: s.tools, Policy: s.policy, Session: session.New("fake", "m", "."), MaxSteps: 2, ToolTimeout: time.Second}
	if err := s.handle(Request{ID: "1", Type: "prompt", SessionID: "s1", Message: "wait"}); err != nil {
		t.Fatal(err)
	}
	if err := s.handle(Request{ID: "2", Type: "prompt", SessionID: "s2", Message: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"2","session_id":"s2"`) })
	if err := s.handle(Request{ID: "3", Type: "set_model", SessionID: "s1", Model: "x"}); err == nil || err.Error() != "cannot change model while a request is running" {
		t.Fatalf("set_model err = %v", err)
	}
	if err := s.handle(Request{ID: "4", Type: "abort", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(buf.String(), `"type":"done","id":"1","session_id":"s1"`) })
}

func TestListModelsWithoutProviderConfigured(t *testing.T) {
	buf := &syncBuffer{}
	s := newTestServer()
	s.enc = NewEncoder(buf)
	rpcSess, _ := s.sessionByID("s1")
	rpcSess.provider = nil
	rpcSess.runtime.Provider = nil
	if err := s.handle(Request{ID: "1", Type: "list_models", SessionID: "s1"}); err == nil || err.Error() != "no provider configured" {
		t.Fatalf("err = %v", err)
	}
}

func TestServeEmitsReadyAndErrorFrames(t *testing.T) {
	s := newTestServer()
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

func TestEventJSONDataField(t *testing.T) {
	data, err := json.Marshal(Event{Type: "x", SessionID: "s1", Data: map[string]string{"a": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"data":{"a":"b"}`) || !strings.Contains(string(data), `"session_id":"s1"`) {
		t.Fatalf("json = %s", string(data))
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
