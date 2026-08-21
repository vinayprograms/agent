// Package swarm provides swarm infrastructure components.
package swarm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vinayprograms/agentkit/tools"
	"github.com/vinayprograms/swarmkit/messaging"
)

// WorkerCapability describes a worker pool available for dispatch.
type WorkerCapability struct {
	Name     string // capability name (e.g., "develop")
	Replicas int    // number of worker instances
}

// DispatchTool allows a manager agent to dispatch tasks to worker capabilities.
// This tool is ONLY registered for agents with type=manager — workers never see it.
type DispatchTool struct {
	bus          messaging.Bus
	managerName  string
	capabilities []WorkerCapability
}

// NewDispatchTool creates a dispatch tool bound to the given message bus.
// capabilities lists the worker pools available for dispatch.
func NewDispatchTool(b messaging.Bus, managerName string, capabilities []WorkerCapability) *DispatchTool {
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

func (t *DispatchTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"capability": {
			Type:        tools.StringParam,
			Description: "The worker capability to route this task to (e.g., \"develop\", \"test\", \"review\").",
			Required:    true,
		},
		"task": {
			Type:        tools.StringParam,
			Description: "Complete, self-contained task description for the worker. Must include everything the worker needs — workers execute in isolation and cannot see other tasks.",
			Required:    true,
		},
	}
}

func (t *DispatchTool) Execute(ctx context.Context, args tools.Args) (string, error) {
	capability := args.StringOr("capability", "")
	task := args.StringOr("task", "")

	if capability == "" {
		return "", fmt.Errorf("capability is required")
	}
	if task == "" {
		return "", fmt.Errorf("task description is required")
	}

	taskID := fmt.Sprintf("t-%s", uuid.New().String()[:8])
	subject := fmt.Sprintf("work.%s.%s", capability, taskID)

	taskMsg := &TaskMessage{
		TaskID:      taskID,
		Capability:  capability,
		Inputs:      map[string]string{"task": task},
		Attempt:     1,
		SubmittedBy: t.managerName,
		SubmittedAt: time.Now(),
	}
	data, err := taskMsg.Marshal()
	if err != nil {
		return "", fmt.Errorf("marshaling task: %w", err)
	}

	if err := t.bus.Publish(subject, data); err != nil {
		return "", fmt.Errorf("publishing to %s: %w", subject, err)
	}

	return fmt.Sprintf("Dispatched task %s to capability %q", taskID, capability), nil
}
