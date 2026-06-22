package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	cfg    *config.Config
	auth   *config.Auth
	policy workspace.Policy
	tools  *tool.Registry

	newProvider func(config.Selection) (provider.Provider, error)

	enc   *Encoder
	encMu sync.Mutex

	mu              sync.Mutex
	sessions        map[string]*rpcSession
	activeSessionID string
	selection       config.Selection // default/fallback selection template
}

type rpcSession struct {
	id        string
	selection config.Selection
	provider  provider.Provider
	runtime   *agent.Runtime

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
		sessions:  map[string]*rpcSession{},
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
	rpcSess, err := s.newRPCSession("", sel)
	if err != nil {
		return nil, err
	}
	s.sessions[rpcSess.id] = rpcSess
	s.activeSessionID = rpcSess.id
	return s, nil
}

// Serve runs the RPC loop using newline-delimited JSON frames.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.enc = NewEncoder(out)
	if err := s.emit(Event{Type: "ready", Data: s.stateData(s.activeSession())}); err != nil {
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
			if encErr := s.emit(Event{Type: "error", ID: req.ID, SessionID: req.SessionID, Error: err.Error()}); encErr != nil {
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
		rpcSess, err := s.sessionForRequest(req.SessionID)
		if err != nil {
			return err
		}
		return s.emit(Event{Type: "state", ID: req.ID, SessionID: rpcSess.id, Data: s.stateData(rpcSess)})
	case "list_sessions":
		return s.emit(Event{Type: "sessions", ID: req.ID, Data: s.sessionsData()})
	case "create_session":
		return s.createSession(req)
	case "use_session":
		return s.useSession(req)
	case "close_session":
		return s.closeSession(req)
	case "list_providers":
		return s.emit(Event{Type: "providers", ID: req.ID, Data: config.ProviderNames(s.cfg)})
	case "list_models":
		rpcSess, err := s.sessionForRequest(req.SessionID)
		if err != nil {
			return err
		}
		models, err := s.listModels(rpcSess)
		if err != nil {
			return err
		}
		return s.emit(Event{Type: "models", ID: req.ID, SessionID: rpcSess.id, Data: models})
	case "set_provider":
		return s.setProvider(req)
	case "set_model":
		return s.setModel(req)
	case "new_session":
		return s.resetSession(req)
	case "abort":
		return s.abort(req)
	case "prompt":
		return s.prompt(req)
	default:
		return fmt.Errorf("unknown command: %s", req.Type)
	}
}

func (s *Server) createSession(req Request) error {
	base := s.activeSession()
	baseSel := s.selection
	if base != nil {
		baseSel = base.selection
	}
	sel, err := config.ResolveSelection(s.cfg, s.auth, valueOr(req.Provider, baseSel.Provider), valueOr(req.Model, baseSel.Model), "")
	if err != nil {
		return err
	}
	rpcSess, err := s.newRPCSession(req.SessionID, sel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.sessions[rpcSess.id]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session already exists: %s", rpcSess.id)
	}
	s.sessions[rpcSess.id] = rpcSess
	if s.activeSessionID == "" {
		s.activeSessionID = rpcSess.id
	}
	s.mu.Unlock()
	return s.emit(Event{Type: "session", ID: req.ID, SessionID: rpcSess.id, Data: s.stateData(rpcSess)})
}

func (s *Server) useSession(req Request) error {
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	rpcSess, err := s.sessionByID(req.SessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.activeSessionID = rpcSess.id
	s.mu.Unlock()
	return s.emit(Event{Type: "state", ID: req.ID, SessionID: rpcSess.id, Data: s.stateData(rpcSess)})
}

func (s *Server) closeSession(req Request) error {
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	busy := rpcSess.busy
	rpcSess.mu.Unlock()
	if busy {
		return fmt.Errorf("cannot close session while a request is running")
	}
	s.mu.Lock()
	delete(s.sessions, rpcSess.id)
	if len(s.sessions) == 0 {
		fresh, freshErr := s.newRPCSession("", s.selection)
		if freshErr != nil {
			s.mu.Unlock()
			return freshErr
		}
		s.sessions[fresh.id] = fresh
		s.activeSessionID = fresh.id
	} else if s.activeSessionID == rpcSess.id {
		ids := make([]string, 0, len(s.sessions))
		for id := range s.sessions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		s.activeSessionID = ids[0]
	}
	active := s.sessions[s.activeSessionID]
	s.mu.Unlock()
	return s.emit(Event{Type: "sessions", ID: req.ID, SessionID: active.id, Data: s.stateData(active)})
}

func (s *Server) prompt(req Request) error {
	if strings.TrimSpace(req.Message) == "" {
		return fmt.Errorf("message is required")
	}
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	if rpcSess.busy {
		rpcSess.mu.Unlock()
		return fmt.Errorf("another request is already running in session %s", rpcSess.id)
	}
	if rpcSess.provider == nil || rpcSess.runtime == nil {
		rpcSess.mu.Unlock()
		return fmt.Errorf("no provider configured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	rpcSess.busy = true
	rpcSess.cancel = cancel
	runtime := rpcSess.runtime
	runtime.OnToolStart = func(call tool.Call) {
		_ = s.emit(Event{Type: "tool_start", ID: req.ID, SessionID: rpcSess.id, Data: map[string]any{"name": call.Name, "args": jsonRaw(call.Args)}})
	}
	runtime.OnToolResult = func(call tool.Call, result tool.Result) {
		_ = s.emit(Event{Type: "tool_result", ID: req.ID, SessionID: rpcSess.id, Data: map[string]any{"name": call.Name, "result": result}})
	}
	rpcSess.mu.Unlock()

	go s.runPrompt(ctx, req.ID, req.Message, rpcSess, runtime)
	return s.emit(Event{Type: "started", ID: req.ID, SessionID: rpcSess.id})
}

func (s *Server) runPrompt(ctx context.Context, id, message string, rpcSess *rpcSession, runtime *agent.Runtime) {
	out, err := runtime.RunOnceStreamObserved(ctx, message, agent.StreamObserver{Delta: func(delta string, reasoning bool) {
		if reasoning || strings.TrimSpace(delta) == "" {
			return
		}
		_ = s.emit(Event{Type: "assistant_delta", ID: id, SessionID: rpcSess.id, Delta: delta})
	}, StepDone: func(bool) {}})

	rpcSess.mu.Lock()
	rpcSess.busy = false
	rpcSess.cancel = nil
	runtime.OnToolStart = nil
	runtime.OnToolResult = nil
	rpcSess.mu.Unlock()

	if err != nil {
		_ = s.emit(Event{Type: "error", ID: id, SessionID: rpcSess.id, Error: err.Error()})
		_ = s.emit(Event{Type: "done", ID: id, SessionID: rpcSess.id})
		return
	}
	_ = s.emit(Event{Type: "assistant_done", ID: id, SessionID: rpcSess.id, Message: out})
	_ = s.emit(Event{Type: "done", ID: id, SessionID: rpcSess.id})
}

func (s *Server) abort(req Request) error {
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	cancel := rpcSess.cancel
	busy := rpcSess.busy
	rpcSess.mu.Unlock()
	if busy && cancel != nil {
		cancel()
	}
	return s.emit(Event{Type: "aborted", ID: req.ID, SessionID: rpcSess.id, Data: map[string]any{"had_active_request": busy}})
}

func (s *Server) resetSession(req Request) error {
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	if rpcSess.busy {
		rpcSess.mu.Unlock()
		return fmt.Errorf("cannot reset session while a request is running")
	}
	if rpcSess.runtime != nil {
		rpcSess.runtime.Session = session.New(rpcSess.selection.Provider, rpcSess.selection.Model, s.policy.Root)
		if strings.TrimSpace(req.SessionID) != "" {
			rpcSess.runtime.Session.ID = rpcSess.id
		}
	}
	rpcSess.mu.Unlock()
	return s.emit(Event{Type: "session", ID: req.ID, SessionID: rpcSess.id, Data: s.stateData(rpcSess)})
}

func (s *Server) setProvider(req Request) error {
	if req.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	busy := rpcSess.busy
	rpcSess.mu.Unlock()
	if busy {
		return fmt.Errorf("cannot change provider while a request is running")
	}
	sel, err := config.ResolveSelection(s.cfg, s.auth, req.Provider, rpcSess.selection.Model, "")
	if err != nil {
		return err
	}
	fresh, err := s.newRPCSession(rpcSess.id, sel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sessions[rpcSess.id] = fresh
	if s.activeSessionID == rpcSess.id {
		s.selection = sel
	}
	s.mu.Unlock()
	return s.emit(Event{Type: "state", ID: req.ID, SessionID: fresh.id, Data: s.stateData(fresh)})
}

func (s *Server) setModel(req Request) error {
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("model is required")
	}
	rpcSess, err := s.sessionForRequest(req.SessionID)
	if err != nil {
		return err
	}
	rpcSess.mu.Lock()
	if rpcSess.busy {
		rpcSess.mu.Unlock()
		return fmt.Errorf("cannot change model while a request is running")
	}
	rpcSess.selection.Model = req.Model
	if rpcSess.provider != nil {
		rpcSess.provider.SetModel(req.Model)
	}
	if rpcSess.runtime != nil && rpcSess.runtime.Session != nil {
		rpcSess.runtime.Session.Model = req.Model
		rpcSess.runtime.Provider = rpcSess.provider
	}
	nextSel := rpcSess.selection
	rpcSess.mu.Unlock()
	s.mu.Lock()
	if s.activeSessionID == rpcSess.id {
		s.selection = nextSel
	}
	s.mu.Unlock()
	return s.emit(Event{Type: "state", ID: req.ID, SessionID: rpcSess.id, Data: s.stateData(rpcSess)})
}

func (s *Server) listModels(rpcSess *rpcSession) ([]string, error) {
	if rpcSess == nil {
		return nil, fmt.Errorf("session not found")
	}
	if rpcSess.provider == nil {
		return nil, fmt.Errorf("no provider configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := rpcSess.provider.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		prefix := " "
		if m.ID == rpcSess.selection.Model {
			prefix = "*"
		}
		out = append(out, prefix+" "+m.ID)
	}
	return out, nil
}

func (s *Server) newRPCSession(id string, sel config.Selection) (*rpcSession, error) {
	if strings.TrimSpace(id) == "" {
		id = s.uniqueSessionID()
	}
	p, err := s.newProvider(sel)
	if err != nil && sel.Provider != "" {
		p = nil
	}
	model := sel.Model
	if p != nil {
		model = p.Model()
	}
	rt := &agent.Runtime{
		Provider:       p,
		Tools:          s.tools,
		Policy:         s.policy,
		Session:        session.New(sel.Provider, model, s.policy.Root),
		ToolTimeout:    time.Duration(s.cfg.Agent.ToolTimeoutSeconds) * time.Second,
		RequestTimeout: time.Duration(s.cfg.Agent.RequestTimeoutSeconds) * time.Second,
	}
	rt.Session.ID = id
	return &rpcSession{id: id, selection: sel, provider: p, runtime: rt}, nil
}

func (s *Server) uniqueSessionID() string {
	base := time.Now().UTC().Format("20060102-150405")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if _, exists := s.sessions[id]; !exists {
			return id
		}
	}
}

func (s *Server) sessionForRequest(id string) (*rpcSession, error) {
	if strings.TrimSpace(id) != "" {
		return s.sessionByID(id)
	}
	rpcSess := s.activeSession()
	if rpcSess == nil {
		return nil, fmt.Errorf("no active session")
	}
	return rpcSess, nil
}

func (s *Server) sessionByID(id string) (*rpcSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rpcSess := s.sessions[strings.TrimSpace(id)]
	if rpcSess == nil {
		return nil, fmt.Errorf("unknown session: %s", id)
	}
	return rpcSess, nil
}

func (s *Server) activeSession() *rpcSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[s.activeSessionID]
}

func (s *Server) stateData(rpcSess *rpcSession) map[string]any {
	data := map[string]any{
		"active_session_id": s.activeSessionID,
		"sessions":          s.sessionsData(),
	}
	if rpcSess == nil {
		return data
	}
	rpcSess.mu.Lock()
	defer rpcSess.mu.Unlock()
	data["session_id"] = rpcSess.id
	data["provider"] = rpcSess.selection.Provider
	data["model"] = rpcSess.selection.Model
	data["workspace"] = s.policy.Root
	data["busy"] = rpcSess.busy
	if rpcSess.runtime != nil && rpcSess.runtime.Session != nil {
		data["message_count"] = len(rpcSess.runtime.Session.Messages)
	}
	return data
}

func (s *Server) sessionsData() []map[string]any {
	s.mu.Lock()
	sessions := make([]*rpcSession, 0, len(s.sessions))
	for _, rpcSess := range s.sessions {
		sessions = append(sessions, rpcSess)
	}
	active := s.activeSessionID
	s.mu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].id < sessions[j].id })
	out := make([]map[string]any, 0, len(sessions))
	for _, rpcSess := range sessions {
		rpcSess.mu.Lock()
		item := map[string]any{
			"session_id":    rpcSess.id,
			"provider":      rpcSess.selection.Provider,
			"model":         rpcSess.selection.Model,
			"workspace":     s.policy.Root,
			"busy":          rpcSess.busy,
			"is_active":     rpcSess.id == active,
			"message_count": 0,
		}
		if rpcSess.runtime != nil && rpcSess.runtime.Session != nil {
			item["message_count"] = len(rpcSess.runtime.Session.Messages)
		}
		rpcSess.mu.Unlock()
		out = append(out, item)
	}
	return out
}

func (s *Server) emit(ev Event) error {
	s.encMu.Lock()
	defer s.encMu.Unlock()
	if s.enc == nil {
		return nil
	}
	return s.enc.Encode(ev)
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

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
