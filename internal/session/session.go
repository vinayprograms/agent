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

	// Observation extraction (semantic memory)
	EventObservation = "observation" // Insights extracted from a completed piece of work (or a failed extraction)

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
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`

	// events is deliberately unexported: it is guarded by mu, and an
	// exported slice is an open invitation to read it without the lock —
	// which races with the background goroutines that add events. Read it
	// through Snapshot. The session is never marshalled as a whole (the
	// on-disk JSONL is built field by field in recorder.go's jsonlRecord),
	// so unexporting costs no serialization.
	events  []Event
	seq     atomic.Uint64 // last issued SeqID
	mu      sync.Mutex    // guards events, UpdatedAt, written
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
	SubAgentName    string            `json:"subagent_name,omitempty"`    // Sub-agent identifier
	SubAgentRole    string            `json:"subagent_role,omitempty"`    // Sub-agent role (from AGENT definition)
	SubAgentModel   string            `json:"subagent_model,omitempty"`   // Resolved model actually used by the sub-agent (e.g. "deepseek-v4-pro:cloud")
	SubAgentProfile string            `json:"subagent_profile,omitempty"` // Profile name requested (e.g. "reasoning-heavy"); may differ from SubAgentModel
	SubAgentTask    string            `json:"subagent_task,omitempty"`    // Task given to sub-agent
	SubAgentOutput  string            `json:"subagent_output,omitempty"`  // Full output from sub-agent
	SubAgentInputs  map[string]string `json:"subagent_inputs,omitempty"`  // Inputs passed to sub-agent

	// LLM details
	Model          string `json:"model,omitempty"`           // Model used
	LatencyMs      int64  `json:"latency_ms,omitempty"`      // LLM call latency
	TokensIn       int    `json:"tokens_in,omitempty"`       // Input tokens
	TokensOut      int    `json:"tokens_out,omitempty"`      // Output tokens
	StopReason     string `json:"stop_reason,omitempty"`     // Why the LLM stopped (e.g. "stop", "length", "tool_calls")
	ThinkingChars  int    `json:"thinking_chars,omitempty"`  // Length of the model's thinking/reasoning text, when separated from content
	TruncatedEmpty bool   `json:"truncated_empty,omitempty"` // This turn hit stop_reason=="length" (or was empty) with no tool calls
	RetriedEmpty   bool   `json:"retried_empty,omitempty"`   // A continuation retry ran after a truncated/empty turn

	// Full LLM interaction (for forensic replay)
	Prompt   string `json:"prompt,omitempty"`   // Full prompt sent to LLM
	Response string `json:"response,omitempty"` // Full LLM response
	Thinking string `json:"thinking,omitempty"` // LLM thinking/reasoning (if available)

	// Non-debug content policy: full assistant/tool_result content is
	// PII-sensitive and only logged under --debug (see Response/Result
	// above and Event.Content). Outside debug mode a run must still be
	// diagnosable and roughly replayable, so every event that withholds
	// full content instead logs a truncated preview plus the full content's
	// byte size and a short hash — enough to tell empty from truncated from
	// huge (e.g. the suspected ~70k-token web_fetch injection), and to
	// confirm two previews came from the same underlying text.
	ContentSize int    `json:"content_size,omitempty"` // Full content length in bytes, regardless of debug mode
	ContentHash string `json:"content_hash,omitempty"` // First 16 hex chars of sha256(full content)

	// Error carries a failure's error text. It exists because Event.Error
	// is shadowed on the wire: jsonlRecord's footer-level "error" field
	// wins over the embedded Event.Error field of the same JSON name when
	// an event record is marshaled, so a tool_result's error never reached
	// the JSONL file. Populate this instead for anything that must survive
	// persistence.
	Error string `json:"error,omitempty"` // Failure text (e.g. a failed tool's error)

	// Goal outcome (goal_end events)
	Retried    bool `json:"retried,omitempty"`    // Whether the goal was retried after a soft failure
	Iterations int  `json:"iterations,omitempty"` // Iteration count for CONVERGE goals (converged or not)

	// Observation extraction (observation events)
	ObservationSource     string `json:"obs_source,omitempty"`      // Who/what produced the extracted work (agent name or role)
	ObservationCount      int    `json:"obs_count,omitempty"`       // Total number of observations extracted
	ObservationFindings   int    `json:"obs_findings,omitempty"`    // Count of "finding" type observations
	ObservationInsights   int    `json:"obs_insights,omitempty"`    // Count of "insight" type observations
	ObservationLessons    int    `json:"obs_lessons,omitempty"`     // Count of "lesson" type observations
	ObservationStoreError string `json:"obs_store_error,omitempty"` // Set if extraction succeeded but storing failed
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
	s.events = append(s.events, events...)
	s.UpdatedAt = time.Now()
}

// Snapshot returns a copy of the events recorded so far, taken under the
// same lock that guards appends.
//
// Events is an exported field, but reading it directly races with any
// concurrent AddEvent — and events are added from background goroutines
// (observation extraction, for one), so a reader that happens to run while
// the session is live is racing whether or not it looks like it. Anything
// reading events off a session that may still be written to goes through
// here.
func (s *Session) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// AppendEvents records events in memory as given, keeping their SeqIDs and
// timestamps rather than assigning new ones the way AddEvent does. It is how
// a session is rebuilt from events that already happened — loading a
// transcript from disk, or constructing one for a consumer like replay.
//
// Use AddEvent for events happening now; this is for events that already
// carry their identity.
func (s *Session) AppendEvents(events ...Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	// Deliberately not touching UpdatedAt: these events already happened,
	// and restoring them is not a modification of the session. Loading a
	// transcript must round-trip the UpdatedAt that was persisted.
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
