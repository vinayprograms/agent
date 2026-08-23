// Package session provides forensic session recording: every LLM call, tool
// execution, and security decision is captured for replay and debugging.
//
// A [Recorder] persists sessions to a directory as append-only JSONL
// (header, event records, footer — the last footer wins on read). Sessions
// created by a Recorder batch their events to disk on a background writer;
// a zero [Session] records in memory only.
package session

import (
	"sync"
	"sync/atomic"
	"time"
)

// Status constants for sessions.
const (
	StatusRunning  = "running"
	StatusComplete = "complete"
	StatusFailed   = "failed"
	StatusAborted  = "aborted"
)

// Event types for the session log - unified forensic events
const (
	// LLM conversation events
	EventSystem    = "system"    // System message to LLM
	EventUser      = "user"      // User/prompt message to LLM
	EventAssistant = "assistant" // LLM response

	// Tool events
	EventToolCall   = "tool_call"   // Tool invocation started
	EventToolResult = "tool_result" // Tool completed

	// Goal events
	EventGoalStart = "goal_start"
	EventGoalEnd   = "goal_end"

	// Workflow events
	EventWorkflowStart = "workflow_start"
	EventWorkflowEnd   = "workflow_end"

	// Supervision events (four-phase execution)
	EventPhaseCommit    = "phase_commit"    // COMMIT phase - agent declares intent
	EventPhaseExecute   = "phase_execute"   // EXECUTE phase - do work
	EventPhaseReconcile = "phase_reconcile" // RECONCILE phase - static checks
	EventPhaseSupervise = "phase_supervise" // SUPERVISE phase - LLM judgment
	EventCheckpoint     = "checkpoint"      // Checkpoint saved

	// Security events
	EventSecurityBlock      = "security_block"      // Untrusted content registered
	EventSecurityStatic     = "security_static"     // Static/deterministic checks (patterns, entropy)
	EventSecurityTriage     = "security_triage"     // LLM triage for suspicious content
	EventSecuritySupervisor = "security_supervisor" // Full supervisor review
	EventSecurityDecision   = "security_decision"   // Final decision
	EventBashSecurity       = "bash_security"       // Bash command security check

	// Sub-agent events
	EventSubAgentStart = "subagent_start" // Sub-agent spawned
	EventSubAgentEnd   = "subagent_end"   // Sub-agent completed

	// Warning events (shown in yellow in replay)
	EventWarning = "warning"
)

// Sink receives every event added to a session created by a [Recorder],
// synchronously from AddEvent, after the event has been sequenced and
// timestamped. Swarm mode uses it to stream events to NATS. It must not
// call back into the session.
type Sink func(Event)

// Session is the persisted record of one workflow execution.
//
// The zero value is usable: AddEvent appends in memory and Flush/Close are
// no-ops. Sessions returned by [Recorder.Create] additionally stream events
// to disk in batches until Close. Safe for concurrent use.
//
// The exported fields (other than Events, which AddEvent owns) are the
// caller's to read and mutate freely; they are only read back by
// [Recorder.Update], which persists whatever they hold at that call.
type Session struct {
	ID           string            `json:"id"`
	WorkflowName string            `json:"workflow_name"`
	Agentfile    string            `json:"agentfile,omitempty"`
	Label        string            `json:"label,omitempty"`
	Inputs       map[string]string `json:"inputs"`
	State        map[string]any    `json:"state"`
	Outputs      map[string]string `json:"outputs"`
	Status       string            `json:"status"`
	Result       string            `json:"result,omitempty"`
	Error        string            `json:"error,omitempty"`
	Events       []Event           `json:"events"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`

	seq     atomic.Uint64 // last issued SeqID
	mu      sync.Mutex    // guards Events, UpdatedAt, written
	written int           // Events already appended to the file
	sink    Sink          // set by Recorder.Create; nil otherwise
	w       *writer       // set by Recorder.Create; nil otherwise
}

// Event represents a single entry in the session log.
// This is THE forensic record - all analysis tools read from here.
type Event struct {
	// Core fields - always present
	SeqID     uint64    `json:"seq"`       // Monotonic sequence number for ordering
	Type      string    `json:"type"`      // Event type (see constants above)
	Timestamp time.Time `json:"timestamp"` // When this event occurred

	// Correlation - for linking related events
	CorrelationID string `json:"corr_id,omitempty"` // Links related events (e.g., tool_call -> security checks -> tool_result)
	ParentSeqID   uint64 `json:"parent,omitempty"`  // Parent event sequence ID (for nesting)

	// Agent context - for sub-agent attribution
	Agent     string `json:"agent,omitempty"`      // Agent name (for sub-agents)
	AgentRole string `json:"agent_role,omitempty"` // Agent role (for dynamic sub-agents)

	// Context - where in execution this happened
	Goal string `json:"goal,omitempty"` // Current goal name
	Step string `json:"step,omitempty"` // Current step (for workflow steps)

	// Content - the actual data
	Content string         `json:"content,omitempty"` // Message content, tool result, etc.
	Tool    string         `json:"tool,omitempty"`    // Tool name (for tool events)
	Args    map[string]any `json:"args,omitempty"`    // Tool arguments (sanitized)

	// Outcome
	Success    *bool  `json:"success,omitempty"`     // nil = in progress, true = success, false = failure
	Error      string `json:"error,omitempty"`       // Error message if failed
	DurationMs int64  `json:"duration_ms,omitempty"` // Execution time (for *_end events)

	// Forensic metadata - structured data for analysis
	Meta *EventMeta `json:"meta,omitempty"`
}

// TaintNode represents a node in the taint dependency tree.
// Each node describes a content block and its relationship to the security event.
type TaintNode struct {
	BlockID   string      `json:"block_id"`             // Block identifier (b0001, etc.)
	Trust     string      `json:"trust"`                // trusted, vetted, untrusted
	Source    string      `json:"source"`               // Where content came from
	EventSeq  uint64      `json:"event_seq,omitempty"`  // Event sequence when block was created
	Depth     int         `json:"depth,omitempty"`      // Depth in the taint tree (0 = root)
	TaintedBy []TaintNode `json:"tainted_by,omitempty"` // Parent blocks that influenced this block
}

// EventMeta contains detailed forensic information.
// Structured for easy querying by forensic tools.
type EventMeta struct {
	// Phase execution (supervision)
	Phase         string   `json:"phase,omitempty"`          // COMMIT, EXECUTE, RECONCILE, SUPERVISE
	Result        string   `json:"result,omitempty"`         // Phase result or verdict
	Commitment    string   `json:"commitment,omitempty"`     // Agent's declared intent (JSON)
	Confidence    string   `json:"confidence,omitempty"`     // high, medium, low
	Triggers      []string `json:"triggers,omitempty"`       // Reconcile triggers that fired
	Escalate      bool     `json:"escalate,omitempty"`       // Whether to escalate to SUPERVISE
	Verdict       string   `json:"verdict,omitempty"`        // CONTINUE, REORIENT, PAUSE
	Correction    string   `json:"correction,omitempty"`     // Supervisor correction text
	Guidance      string   `json:"guidance,omitempty"`       // Supervisor guidance (alias for correction)
	Human         bool     `json:"human,omitempty"`          // Human intervention required/occurred
	HumanRequired bool     `json:"human_required,omitempty"` // Alias for Human field

	// Supervisor identification
	SupervisorType string `json:"supervisor_type,omitempty"` // "execution" or "security"

	// Security
	BlockID       string      `json:"block_id,omitempty"`       // Content block ID (b0001, b0002, ...)
	RelatedBlocks []string    `json:"related_blocks,omitempty"` // All blocks whose content contributed to this action
	TaintLineage  []TaintNode `json:"taint_lineage,omitempty"`  // Taint dependency tree for security events
	Trust         string      `json:"trust,omitempty"`          // trusted, vetted, untrusted
	BlockType     string      `json:"block_type,omitempty"`     // instruction, data
	Source        string      `json:"source,omitempty"`         // Where content came from
	Entropy       float64     `json:"entropy,omitempty"`        // Shannon entropy (0.0-8.0)
	CheckName     string      `json:"check,omitempty"`          // static, triage, supervisor
	Pass          bool        `json:"pass,omitempty"`           // Check passed
	Flags         []string    `json:"flags,omitempty"`          // Security flags detected
	Suspicious    bool        `json:"suspicious,omitempty"`     // Triage result
	Action        string      `json:"action,omitempty"`         // allow, deny, modify
	Reason        string      `json:"reason,omitempty"`         // Decision reason
	CheckPath     string      `json:"check_path,omitempty"`     // Verification path (static, static→triage, static→triage→supervisor)
	SkipReason    string      `json:"skip_reason,omitempty"`    // Why escalation was skipped (e.g., "low_risk_tool", "no_untrusted_content", "triage_benign")
	XMLBlock      string      `json:"xml,omitempty"`            // Full XML block for forensic tools

	// Deprecated: use CheckName/CheckPath instead
	Tier     int    `json:"tier,omitempty"`      // 1=static, 2=triage, 3=supervisor
	Tiers    string `json:"tiers,omitempty"`     // Old tier path format
	TierPath string `json:"tier_path,omitempty"` // Alias for Tiers

	// Checkpoint
	CheckpointType string `json:"ckpt_type,omitempty"` // pre, post, reconcile, supervise
	CheckpointID   string `json:"ckpt_id,omitempty"`   // Checkpoint identifier

	// Sub-agent execution
	SubAgentName   string            `json:"subagent_name,omitempty"`   // Sub-agent identifier
	SubAgentRole   string            `json:"subagent_role,omitempty"`   // Sub-agent role (from AGENT definition)
	SubAgentModel  string            `json:"subagent_model,omitempty"`  // Model used by sub-agent
	SubAgentTask   string            `json:"subagent_task,omitempty"`   // Task given to sub-agent
	SubAgentOutput string            `json:"subagent_output,omitempty"` // Full output from sub-agent
	SubAgentInputs map[string]string `json:"subagent_inputs,omitempty"` // Inputs passed to sub-agent

	// LLM details
	Model     string `json:"model,omitempty"`      // Model used
	LatencyMs int64  `json:"latency_ms,omitempty"` // LLM call latency
	TokensIn  int    `json:"tokens_in,omitempty"`  // Input tokens
	TokensOut int    `json:"tokens_out,omitempty"` // Output tokens

	// Full LLM interaction (for forensic replay)
	Prompt   string `json:"prompt,omitempty"`   // Full prompt sent to LLM
	Response string `json:"response,omitempty"` // Full LLM response
	Thinking string `json:"thinking,omitempty"` // LLM thinking/reasoning (if available)

	// Error carries a failure's error text. It exists because Event.Error
	// is shadowed on the wire: jsonlRecord's footer-level "error" field
	// wins over the embedded Event.Error field of the same JSON name when
	// an event record is marshaled, so a tool_result's error never reached
	// the JSONL file. Populate this instead for anything that must survive
	// persistence.
	Error string `json:"error,omitempty"` // Failure text (e.g. a failed tool's error)
}

// AddEvent sequences and timestamps event (if its Timestamp is zero),
// hands it to the Sink, and records it. It never blocks on a closed
// session: before Close events are batched to the writer; after Close (or
// on a zero Session) they are appended in memory, where a later
// [Recorder.Update] persists them. Returns the assigned SeqID.
func (s *Session) AddEvent(event Event) uint64 {
	event.SeqID = s.seq.Add(1)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if s.sink != nil {
		s.sink(event)
	}
	if s.w == nil || !s.w.enqueue(event) {
		s.append(event)
	}
	return event.SeqID
}

// append records events in memory.
func (s *Session) append(events ...Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, events...)
	s.UpdatedAt = time.Now()
}

// Flush blocks until every event added so far is on disk. No-op after
// Close or on a session without a writer.
func (s *Session) Flush() {
	if s.w != nil {
		s.w.flush()
	}
}

// Close flushes remaining events and stops the background writer.
// Idempotent; a no-op on a session without a writer.
func (s *Session) Close() {
	if s.w != nil {
		s.w.close()
	}
}

// StartCorrelation returns a fresh correlation ID for linking related events.
func (s *Session) StartCorrelation() string {
	return randomHex(4)
}
