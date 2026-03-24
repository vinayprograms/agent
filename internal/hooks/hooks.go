// Package hooks provides an event-based hook system for cross-cutting concerns.
//
// Instead of individual callback fields on the executor, hooks allow multiple
// listeners to subscribe to events at various points in the execution lifecycle.
// This follows the middleware pattern from web frameworks: logging, telemetry,
// metrics, and other concerns register as hooks without modifying core logic.
package hooks

import (
	"context"
	"sync"
)

// Standard event types for the execution lifecycle.
const (
	GoalStart        = "goal.start"
	GoalComplete     = "goal.complete"
	ToolCall         = "tool.call"
	ToolError        = "tool.error"
	LLMError         = "llm.error"
	SkillLoaded      = "skill.loaded"
	MCPToolCall      = "mcp.tool.call"
	SubAgentStart    = "subagent.start"
	SubAgentComplete = "subagent.complete"
	SupervisionEvent = "supervision.event"
)

// Event carries data for a hook invocation.
type Event struct {
	Type string
	Data map[string]any
}

// Hook is a function called when an event fires.
type Hook func(ctx context.Context, event Event)

// Registry holds hooks organized by event type.
// It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	hooks map[string][]Hook
}

// NewRegistry creates an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{
		hooks: make(map[string][]Hook),
	}
}

// On registers a hook for the given event type.
func (r *Registry) On(eventType string, hook Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[eventType] = append(r.hooks[eventType], hook)
}

// Fire invokes all hooks registered for the given event type.
// Hooks are called in registration order. A nil registry is safe to call.
func (r *Registry) Fire(ctx context.Context, eventType string, data map[string]any) {
	if r == nil {
		return
	}
	r.mu.RLock()
	handlers := r.hooks[eventType]
	r.mu.RUnlock()

	if len(handlers) == 0 {
		return
	}

	event := Event{Type: eventType, Data: data}
	for _, h := range handlers {
		h(ctx, event)
	}
}
