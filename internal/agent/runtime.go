package agent

import (
	"context"
	"errors"

	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

// ErrNotImplemented marks scaffold-only behavior.
var ErrNotImplemented = errors.New("not implemented yet")

// Runtime is the core agent runtime boundary.
type Runtime struct {
	Provider provider.Provider
	Tools    *tool.Registry
	Policy   workspace.Policy
	Session  *session.Session
	MaxSteps int
}

// RunOnce will execute one user request through the agent loop.
func (r *Runtime) RunOnce(context.Context, string) (string, error) {
	if r == nil {
		return "", errors.New("nil runtime")
	}
	if r.Provider == nil {
		return "", errors.New("provider not configured")
	}
	return "", ErrNotImplemented
}
