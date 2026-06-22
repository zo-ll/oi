package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

// Server is the stdio RPC runtime for oi.
type Server struct {
	cfg       *config.Config
	auth      *config.Auth
	selection config.Selection

	policy   workspace.Policy
	tools    *tool.Registry
	provider provider.Provider
	runtime  *agent.Runtime

	newProvider func(config.Selection) (provider.Provider, error)

	enc   *Encoder
	encMu sync.Mutex

	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
}

// NewServer constructs an RPC server from on-disk config.
func NewServer() (*Server, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, err
	}
	sel, err := config.ResolveSelection(cfg, auth, "", "", "")
	if err != nil {
		return nil, err
	}
	root, err := workspace.DetectRoot("")
	if err != nil {
		return nil, err
	}
	policy := workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalMode(cfg.Agent.ApprovalMode)}
	s := &Server{
		cfg:       cfg,
		auth:      auth,
		selection: sel,
		policy:    policy,
		newProvider: func(sel config.Selection) (provider.Provider, error) {
			if sel.Provider == "" {
				return nil, nil
			}
			return provider.NewForSelection(sel)
		},
	}
	s.tools = tool.NewBuiltinRegistry(tool.Options{
		Policy:         policy,
		MaxOutputBytes: cfg.Agent.MaxToolOutputBytes,
		PromptInput:    nil,
		PromptOutput:   nil,
	})
	if err := s.resetRuntime(); err != nil {
		return nil, err
	}
	return s, nil
}

// Serve runs the RPC loop using newline-delimited JSON frames.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.enc = NewEncoder(out)
	if err := s.emit(Event{Type: "ready", Data: s.stateData()}); err != nil {
		return err
	}
	dec := NewDecoder(in)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := s.handle(req); err != nil {
			if encErr := s.emit(Event{Type: "error", ID: req.ID, Error: err.Error()}); encErr != nil {
				return encErr
			}
		}
	}
}

func (s *Server) handle(req Request) error {
	switch req.Type {
	case "ping":
		return s.emit(Event{Type: "pong", ID: req.ID})
	case "get_state":
		return s.emit(Event{Type: "state", ID: req.ID, Data: s.stateData()})
	case "list_providers":
		return s.emit(Event{Type: "providers", ID: req.ID, Data: config.ProviderNames(s.cfg)})
	case "list_models":
		models, err := s.listModels()
		if err != nil {
			return err
		}
		return s.emit(Event{Type: "models", ID: req.ID, Data: models})
	case "set_provider":
		return s.setProvider(req)
	case "set_model":
		return s.setModel(req)
	case "new_session":
		return s.newSession(req)
	case "abort":
		return s.abort(req)
	case "prompt":
		return s.prompt(req)
	default:
		return fmt.Errorf("unknown command: %s", req.Type)
	}
}

func (s *Server) prompt(req Request) error {
	if strings.TrimSpace(req.Message) == "" {
		return fmt.Errorf("message is required")
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("another request is already running")
	}
	if s.provider == nil || s.runtime == nil {
		s.mu.Unlock()
		return fmt.Errorf("no provider configured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.busy = true
	s.cancel = cancel
	runtime := s.runtime
	runtime.OnToolStart = func(call tool.Call) {
		_ = s.emit(Event{Type: "tool_start", ID: req.ID, Data: map[string]any{"name": call.Name, "args": jsonRaw(call.Args)}})
	}
	runtime.OnToolResult = func(call tool.Call, result tool.Result) {
		_ = s.emit(Event{Type: "tool_result", ID: req.ID, Data: map[string]any{"name": call.Name, "result": result}})
	}
	s.mu.Unlock()

	go s.runPrompt(ctx, req.ID, req.Message, runtime)
	return s.emit(Event{Type: "started", ID: req.ID})
}

func (s *Server) runPrompt(ctx context.Context, id, message string, runtime *agent.Runtime) {
	out, err := runtime.RunOnceStreamObserved(ctx, message, agent.StreamObserver{Delta: func(delta string, reasoning bool) {
		if reasoning || strings.TrimSpace(delta) == "" {
			return
		}
		_ = s.emit(Event{Type: "assistant_delta", ID: id, Delta: delta})
	}, StepDone: func(bool) {}})

	s.mu.Lock()
	s.busy = false
	s.cancel = nil
	runtime.OnToolStart = nil
	runtime.OnToolResult = nil
	s.mu.Unlock()

	if err != nil {
		_ = s.emit(Event{Type: "error", ID: id, Error: err.Error()})
		_ = s.emit(Event{Type: "done", ID: id})
		return
	}
	_ = s.emit(Event{Type: "assistant_done", ID: id, Message: out})
	_ = s.emit(Event{Type: "done", ID: id})
}

func (s *Server) abort(req Request) error {
	s.mu.Lock()
	cancel := s.cancel
	busy := s.busy
	s.mu.Unlock()
	if busy && cancel != nil {
		cancel()
	}
	return s.emit(Event{Type: "aborted", ID: req.ID, Data: map[string]any{"had_active_request": busy}})
}

func (s *Server) newSession(req Request) error {
	if s.isBusy() {
		return fmt.Errorf("cannot reset session while a request is running")
	}
	s.mu.Lock()
	if s.runtime != nil {
		s.runtime.Session = session.New(s.selection.Provider, s.selection.Model, s.policy.Root)
	}
	s.mu.Unlock()
	return s.emit(Event{Type: "session", ID: req.ID, Data: s.stateData()})
}

func (s *Server) setProvider(req Request) error {
	if req.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if s.isBusy() {
		return fmt.Errorf("cannot change provider while a request is running")
	}
	sel, err := config.ResolveSelection(s.cfg, s.auth, req.Provider, s.selection.Model, "")
	if err != nil {
		return err
	}
	s.selection = sel
	if err := s.resetRuntime(); err != nil {
		return err
	}
	return s.emit(Event{Type: "state", ID: req.ID, Data: s.stateData()})
}

func (s *Server) setModel(req Request) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if s.isBusy() {
		return fmt.Errorf("cannot change model while a request is running")
	}
	s.selection.Model = req.Model
	if s.provider != nil {
		s.provider.SetModel(req.Model)
	}
	s.mu.Lock()
	if s.runtime != nil && s.runtime.Session != nil {
		s.runtime.Session.Model = req.Model
	}
	s.mu.Unlock()
	return s.emit(Event{Type: "state", ID: req.ID, Data: s.stateData()})
}

func (s *Server) listModels() ([]string, error) {
	if s.provider == nil {
		if err := s.resetRuntime(); err != nil {
			return nil, err
		}
	}
	if s.provider == nil {
		return nil, fmt.Errorf("no provider configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := s.provider.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		prefix := " "
		if m.ID == s.selection.Model {
			prefix = "*"
		}
		out = append(out, prefix+" "+m.ID)
	}
	return out, nil
}

func (s *Server) resetRuntime() error {
	p, err := s.newProvider(s.selection)
	if err != nil && s.selection.Provider != "" {
		p = nil
	}
	s.provider = p
	model := s.selection.Model
	if p != nil {
		model = p.Model()
	}
	s.mu.Lock()
	s.runtime = &agent.Runtime{
		Provider:       p,
		Tools:          s.tools,
		Policy:         s.policy,
		Session:        session.New(s.selection.Provider, model, s.policy.Root),
		ToolTimeout:    time.Duration(s.cfg.Agent.ToolTimeoutSeconds) * time.Second,
		RequestTimeout: time.Duration(s.cfg.Agent.RequestTimeoutSeconds) * time.Second,
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) stateData() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := map[string]any{
		"provider":  s.selection.Provider,
		"model":     s.selection.Model,
		"workspace": s.policy.Root,
		"busy":      s.busy,
	}
	if s.runtime != nil && s.runtime.Session != nil {
		data["session_id"] = s.runtime.Session.ID
		data["message_count"] = len(s.runtime.Session.Messages)
	}
	return data
}

func (s *Server) emit(ev Event) error {
	s.encMu.Lock()
	defer s.encMu.Unlock()
	if s.enc == nil {
		return nil
	}
	return s.enc.Encode(ev)
}

func (s *Server) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

func jsonRaw(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return string(raw)
}
