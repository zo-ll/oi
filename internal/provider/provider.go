package provider

import (
	"context"
	"encoding/json"
)

// Message is one chat message in provider-neutral form.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolSpec describes one tool exposed to the model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Request is the normalized provider request shape.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
	Stream   bool      `json:"stream,omitempty"`
}

// ToolCall is one normalized tool invocation from the model.
type ToolCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Usage captures token accounting when a provider returns it.
type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Response is the normalized non-streaming provider response.
type Response struct {
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage,omitempty"`
}

// Model describes a provider model.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// EventType classifies streaming events.
type EventType string

const (
	EventDelta    EventType = "delta"
	EventToolCall EventType = "tool_call"
	EventDone     EventType = "done"
)

// Event is one normalized streaming event.
type Event struct {
	Type     EventType `json:"type"`
	Delta    string    `json:"delta,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Done     bool      `json:"done,omitempty"`
	Err      error     `json:"-"`
}

// Provider is the core backend abstraction for oi.
type Provider interface {
	Name() string
	Model() string
	SetModel(string)
	Chat(context.Context, Request) (Response, error)
	ChatStream(context.Context, Request) (<-chan Event, error)
	ListModels(context.Context) ([]Model, error)
}
