// Copied VERBATIM from agentkit v0.2.1 (tasks/message.go) to keep the swarm
// wire format stable: agentkit v1 dropped the tasks package and swarmkit's
// task envelopes rename JSON fields (task_id->id, agent_id->agent, ...).
// JSON tags, field names and helper signatures below are byte-identical to
// the originals so existing workers, the swarm CLI/UI and on-disk
// ~/.swarm/tasks/*.json files keep interoperating.
//
// TaskMessage is the wire format for tasks sent between orchestrators and agents.
// It includes routing, execution control, and chain context for multi-agent workflows.

package swarm

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrInvalidTask is returned by Validate when a task message is missing required fields.
var ErrInvalidTask = errors.New("invalid task")

// TaskMessage is the envelope for tasks sent to agents.
// It wraps the actual inputs with metadata for routing, tracing, and execution control.
type TaskMessage struct {
	// Identity & Correlation
	TaskID         string `json:"task_id"`                   // Unique identifier for this task instance
	IdempotencyKey string `json:"idempotency_key,omitempty"` // Same key = same logical work (for dedup)
	CorrelationID  string `json:"correlation_id,omitempty"`  // Trace ID spanning entire request chain
	ParentTaskID   string `json:"parent_task_id,omitempty"`  // Task that spawned this one
	RootTaskID     string `json:"root_task_id,omitempty"`    // Original request that started chain

	// Routing
	Capability string `json:"capability"`         // Which capability/skill to invoke
	ReplyTo    string `json:"reply_to,omitempty"` // Subject/URL to publish result

	// Execution Control
	TimeoutSeconds int `json:"timeout_seconds,omitempty"` // Max execution time (0 = use default)
	MaxAttempts    int `json:"max_attempts,omitempty"`    // Retry limit (0 = no retries)
	Attempt        int `json:"attempt,omitempty"`         // Current attempt number (1-indexed)

	// Payload
	Inputs map[string]string `json:"inputs"` // Task parameters (maps to Agentfile INPUTs)

	// Chain Context
	PriorOutputs map[string]json.RawMessage `json:"prior_outputs,omitempty"` // Outputs from upstream agents

	// Metadata
	CreatedAt   time.Time         `json:"created_at"`
	SubmittedAt time.Time         `json:"submitted_at,omitempty"` // When task was submitted (for elapsed tracking)
	SubmittedBy string            `json:"submitted_by,omitempty"` // Orchestrator/user ID
	Tags        []string          `json:"tags,omitempty"`         // Labels for filtering
	Metadata    map[string]string `json:"metadata,omitempty"`     // Arbitrary key-value pairs
}

// Validate reports whether the task message has its required fields.
func (m *TaskMessage) Validate() error {
	if m.TaskID == "" || m.Capability == "" {
		return ErrInvalidTask
	}
	return nil
}

// Marshal serializes the task message to JSON.
func (m *TaskMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// UnmarshalTaskMessage deserializes a task message from JSON.
func UnmarshalTaskMessage(data []byte) (*TaskMessage, error) {
	var m TaskMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ResultStatus represents the outcome of a task.
type ResultStatus string

const (
	ResultSuccess ResultStatus = "success"
	ResultFailed  ResultStatus = "failed"
	ResultTimeout ResultStatus = "timeout"
)

// TaskResult is the response from an agent after executing a task.
type TaskResult struct {
	// Identity
	TaskID        string `json:"task_id"`
	CorrelationID string `json:"correlation_id,omitempty"`

	// Outcome
	Status  ResultStatus `json:"status"`
	Outputs any          `json:"outputs,omitempty"` // Structured outputs from the workflow
	Error   string       `json:"error,omitempty"`   // Error message if failed

	// Execution Info
	AgentID     string    `json:"agent_id"`    // Which agent executed
	Attempt     int       `json:"attempt"`     // Which attempt this was
	DurationMs  int64     `json:"duration_ms"` // Execution time
	CompletedAt time.Time `json:"completed_at"`

	// Metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Marshal serializes the task result to JSON.
func (r *TaskResult) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalTaskResult deserializes a task result from JSON.
func UnmarshalTaskResult(data []byte) (*TaskResult, error) {
	var r TaskResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// NewTaskMessage creates a new task message with required fields.
func NewTaskMessage(taskID, capability string, inputs map[string]string) *TaskMessage {
	return &TaskMessage{
		TaskID:     taskID,
		Capability: capability,
		Inputs:     inputs,
		CreatedAt:  time.Now(),
		Attempt:    1,
	}
}

// NewTaskResult creates a new task result.
func NewTaskResult(taskID, agentID string, status ResultStatus) *TaskResult {
	return &TaskResult{
		TaskID:      taskID,
		AgentID:     agentID,
		Status:      status,
		CompletedAt: time.Now(),
	}
}
