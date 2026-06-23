// Package tool defines the tool execution framework: Tool/Registry for
// registration and invocation, Call/Result for the execution contract.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Spec describes a tool in a model-facing form.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Call is one structured tool invocation.
type Call struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Result is the structured output of a tool execution.
type Result struct {
	Tool   string            `json:"tool"`
	OK     bool              `json:"ok"`
	Output string            `json:"output,omitempty"`
	Error  string            `json:"error,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

// Tool is implemented by every runtime tool.
type Tool interface {
	Name() string
	Spec() Spec
	Run(context.Context, Call) Result
}

// Registry holds tools by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry builds a tool registry.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

// Register adds or replaces a tool by name.
func (r *Registry) Register(t Tool) {
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[t.Name()] = t
}

// Lookup returns a tool by name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns all tool specs sorted by name.
func (r *Registry) Specs() []Spec {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Spec, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Spec())
	}
	return out
}

// Run executes one call against the registry.
func (r *Registry) Run(ctx context.Context, call Call) Result {
	if r == nil {
		return Result{Tool: call.Name, Error: "tool registry is nil"}
	}
	t, ok := r.Lookup(call.Name)
	if !ok {
		return Result{Tool: call.Name, Error: fmt.Sprintf("unknown tool: %s", call.Name)}
	}
	res := t.Run(ctx, call)
	if res.Tool == "" {
		res.Tool = call.Name
	}
	if res.Error == "" {
		res.OK = true
	}
	return res
}
