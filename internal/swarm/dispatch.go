// Package swarm provides swarm infrastructure components.
package swarm

import (
	"context"
	"fmt"
	"time"

	"github.com/vinayprograms/agentkit/bus"
	"github.com/vinayprograms/agentkit/tasks"
)

// WorkerCapability describes a worker pool available for dispatch.
type WorkerCapability struct {
	Name     string // capability name (e.g., "develop")
	Replicas int    // number of worker instances
}

// DispatchTool allows a manager agent to dispatch tasks to worker capabilities.
// This tool is ONLY registered for agents with type=manager — workers never see it.
type DispatchTool struct {
	bus          bus.MessageBus
	managerName  string
	capabilities []WorkerCapability
}

// NewDispatchTool creates a dispatch tool bound to the given message bus.
// capabilities lists the worker pools available for dispatch.
func NewDispatchTool(b bus.MessageBus, managerName string, capabilities []WorkerCapability) *DispatchTool {
	return &DispatchTool{bus: b, managerName: managerName, capabilities: capabilities}
}

func (t *DispatchTool) Name() string { return "dispatch" }

func (t *DispatchTool) Description() string {
	desc := `Dispatch a task to a worker capability channel. The task will be picked up by an available worker agent that handles the specified capability. Each call dispatches exactly one task — call multiple times to dispatch multiple tasks.`
	if len(t.capabilities) > 0 {
		desc += "\n\nAvailable capabilities:"
		for _, cap := range t.capabilities {
			desc += fmt.Sprintf("\n- %q (%d workers)", cap.Name, cap.Replicas)
		}
	}
	return desc
}

func (t *DispatchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"capability": map[string]interface{}{
				"type":        "string",
				"description": "The worker capability to route this task to (e.g., \"develop\", \"test\", \"review\").",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "Complete, self-contained task description for the worker. Must include everything the worker needs — workers execute in isolation and cannot see other tasks.",
			},
		},
		"required": []string{"capability", "task"},
	}
}

func (t *DispatchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	capability, _ := args["capability"].(string)
	task, _ := args["task"].(string)

	if capability == "" {
		return nil, fmt.Errorf("capability is required")
	}
	if task == "" {
		return nil, fmt.Errorf("task description is required")
	}

	taskID := fmt.Sprintf("t-%d", time.Now().UnixNano()/1e6)
	subject := fmt.Sprintf("work.%s.%s", capability, taskID)

	taskMsg := &tasks.TaskMessage{
		TaskID:      taskID,
		Capability:  capability,
		Inputs:      map[string]string{"task": task},
		Attempt:     1,
		SubmittedBy: t.managerName,
		SubmittedAt: time.Now(),
	}
	data, err := taskMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshaling task: %w", err)
	}

	if err := t.bus.Publish(subject, data); err != nil {
		return nil, fmt.Errorf("publishing to %s: %w", subject, err)
	}

	return fmt.Sprintf("Dispatched task %s to capability %q", taskID, capability), nil
}
