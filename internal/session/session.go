// Package session provides session management and persistence.
package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Status constants for sessions.
const (
	StatusRunning  = "running"
	StatusComplete = "complete"
	StatusFailed   = "failed"
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
	EventStepStart     = "step_start"
	EventStepEnd       = "step_end"

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

	// Sub-agent events
	EventSubAgentStart = "subagent_start" // Sub-agent spawned
	EventSubAgentEnd   = "subagent_end"   // Sub-agent completed

	// Deprecated: use the descriptive names above
	EventSecurityTier1 = EventSecurityStatic
	EventSecurityTier2 = EventSecurityTriage
	EventSecurityTier3 = EventSecuritySupervisor
)

// Session represents a workflow execution session.
type Session struct {
	ID           string                 `json:"id"`
	WorkflowName string                 `json:"workflow_name"`
	Inputs       map[string]string      `json:"inputs"`
	State        map[string]interface{} `json:"state"`
	Outputs      map[string]string      `json:"outputs"`
	Status       string                 `json:"status"`
	Result       string                 `json:"result,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Events       []Event                `json:"events"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`

	// Internal state (not persisted)
	seqCounter uint64    // Monotonic sequence counter
	mu         sync.Mutex
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
	Content string                 `json:"content,omitempty"` // Message content, tool result, etc.
	Tool    string                 `json:"tool,omitempty"`    // Tool name (for tool events)
	Args    map[string]interface{} `json:"args,omitempty"`    // Tool arguments (sanitized)

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
	BlockID       string   `json:"block_id,omitempty"`       // Content block ID (b0001, b0002, ...)
	RelatedBlocks []string `json:"related_blocks,omitempty"` // All blocks whose content contributed to this action
	TaintLineage  []TaintNode `json:"taint_lineage,omitempty"` // Taint dependency tree for security events
	Trust         string   `json:"trust,omitempty"`          // trusted, vetted, untrusted
	BlockType     string   `json:"block_type,omitempty"`     // instruction, data
	Source        string   `json:"source,omitempty"`         // Where content came from
	Entropy       float64  `json:"entropy,omitempty"`        // Shannon entropy (0.0-8.0)
	CheckName     string   `json:"check,omitempty"`          // static, triage, supervisor
	Pass          bool     `json:"pass,omitempty"`           // Check passed
	Flags         []string `json:"flags,omitempty"`          // Security flags detected
	Suspicious    bool     `json:"suspicious,omitempty"`     // Triage result
	Action        string   `json:"action,omitempty"`         // allow, deny, modify
	Reason        string   `json:"reason,omitempty"`         // Decision reason
	CheckPath     string   `json:"check_path,omitempty"`     // Verification path (static, static→triage, static→triage→supervisor)
	SkipReason    string   `json:"skip_reason,omitempty"`    // Why escalation was skipped (e.g., "low_risk_tool", "no_untrusted_content", "triage_benign")
	XMLBlock   string   `json:"xml,omitempty"`        // Full XML block for forensic tools

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
}

// nextSeqID returns the next sequence ID for this session.
func (s *Session) nextSeqID() uint64 {
	return atomic.AddUint64(&s.seqCounter, 1)
}

// CurrentSeqID returns the current (last used) sequence ID without incrementing.
// Returns 0 if no events have been added yet.
func (s *Session) CurrentSeqID() uint64 {
	return atomic.LoadUint64(&s.seqCounter)
}

// AddEvent adds a new event to the session with automatic sequencing.
func (s *Session) AddEvent(event Event) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	event.SeqID = s.nextSeqID()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	s.Events = append(s.Events, event)
	s.UpdatedAt = time.Now()
	return event.SeqID
}

// StartCorrelation generates a new correlation ID for linking related events.
func (s *Session) StartCorrelation() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Message represents an LLM message (kept for backwards compatibility).
type Message struct {
	Role      string    `json:"role"` // user, assistant, tool
	Content   string    `json:"content"`
	Goal      string    `json:"goal"`
	Agent     string    `json:"agent,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ToolCall represents a tool invocation (kept for backwards compatibility).
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Args      map[string]interface{} `json:"args"`
	Result    interface{}            `json:"result"`
	Error     string                 `json:"error,omitempty"`
	Duration  time.Duration          `json:"duration"`
	Goal      string                 `json:"goal"`
	Timestamp time.Time              `json:"timestamp"`
}

// Store is the interface for session persistence.
type Store interface {
	Save(sess *Session) error
	Load(id string) (*Session, error)
}

// SessionManager is the interface for session management operations.
type SessionManager interface {
	Create(workflowName string) (*Session, error)
	Update(sess *Session) error
	Get(id string) (*Session, error)
}

// Manager manages sessions.
type Manager struct {
	store Store
	mu    sync.Mutex
}

// NewManager creates a new session manager.
func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

// Create creates a new session.
func (m *Manager) Create(workflowName string, inputs map[string]string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := generateID()
	now := time.Now()

	sess := &Session{
		ID:           id,
		WorkflowName: workflowName,
		Inputs:       inputs,
		State:        make(map[string]interface{}),
		Outputs:      make(map[string]string),
		Status:       StatusRunning,
		Events:       []Event{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := m.store.Save(sess); err != nil {
		return nil, err
	}

	return sess, nil
}

// Get retrieves a session by ID.
func (m *Manager) Get(id string) (*Session, error) {
	return m.store.Load(id)
}

// Update saves changes to a session.
func (m *Manager) Update(sess *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess.UpdatedAt = time.Now()
	return m.store.Save(sess)
}

// AddEvent adds an event to a session.
func (m *Manager) AddEvent(id string, event Event) error {
	sess, err := m.store.Load(id)
	if err != nil {
		return err
	}

	sess.AddEvent(event)
	return m.store.Save(sess)
}

// generateID creates a unique session ID.
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// JSONL record types for streaming format
const (
	RecordTypeHeader = "header" // Session metadata (first line)
	RecordTypeEvent  = "event"  // Individual event
	RecordTypeFooter = "footer" // Final state (last line, optional)
)

// JSONLRecord is a wrapper for JSONL lines with type discrimination.
type JSONLRecord struct {
	RecordType string `json:"_type"` // header, event, footer
	
	// Header fields (when _type == "header")
	ID           string            `json:"id,omitempty"`
	WorkflowName string            `json:"workflow_name,omitempty"`
	Inputs       map[string]string `json:"inputs,omitempty"`
	CreatedAt    time.Time         `json:"created_at,omitempty"`
	
	// Event fields (when _type == "event") - embedded Event
	*Event `json:",omitempty"`
	
	// Footer fields (when _type == "footer")
	Status    string                 `json:"status,omitempty"`
	Result    string                 `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Outputs   map[string]string      `json:"outputs,omitempty"`
	State     map[string]interface{} `json:"state,omitempty"`
	UpdatedAt time.Time              `json:"updated_at,omitempty"`
}

// FileStore implements Store using the filesystem.
// New sessions use JSONL format; legacy JSON format is supported for reading.
type FileStore struct {
	dir string
}

// NewFileStore creates a new file-based store.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

// Save persists a session to disk in JSONL format.
func (s *FileStore) Save(sess *Session) error {
	// Ensure directory exists
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	path := filepath.Join(s.dir, sess.ID+".jsonl")
	
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create session file: %w", err)
	}
	defer f.Close()

	// Write header
	header := JSONLRecord{
		RecordType:   RecordTypeHeader,
		ID:           sess.ID,
		WorkflowName: sess.WorkflowName,
		Inputs:       sess.Inputs,
		CreatedAt:    sess.CreatedAt,
	}
	if err := s.writeLine(f, header); err != nil {
		return err
	}

	// Write each event
	for _, evt := range sess.Events {
		evtCopy := evt // copy to avoid pointer issues
		record := JSONLRecord{
			RecordType: RecordTypeEvent,
			Event:      &evtCopy,
		}
		if err := s.writeLine(f, record); err != nil {
			return err
		}
	}

	// Write footer
	footer := JSONLRecord{
		RecordType: RecordTypeFooter,
		Status:     sess.Status,
		Result:     sess.Result,
		Error:      sess.Error,
		Outputs:    sess.Outputs,
		State:      sess.State,
		UpdatedAt:  sess.UpdatedAt,
	}
	if err := s.writeLine(f, footer); err != nil {
		return err
	}

	return nil
}

// writeLine writes a single JSONL record.
func (s *FileStore) writeLine(f *os.File, record JSONLRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if _, err := f.WriteString("\n"); err != nil {
		return err
	}
	return nil
}

// Load reads a session from disk.
// Supports both JSONL (new) and JSON (legacy) formats.
func (s *FileStore) Load(id string) (*Session, error) {
	// Try JSONL first
	jsonlPath := filepath.Join(s.dir, id+".jsonl")
	if _, err := os.Stat(jsonlPath); err == nil {
		return s.loadJSONL(jsonlPath)
	}

	// Fall back to legacy JSON
	jsonPath := filepath.Join(s.dir, id+".json")
	return s.loadLegacyJSON(jsonPath)
}

// loadJSONL loads a session from JSONL format.
func (s *FileStore) loadJSONL(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sess := &Session{
		Inputs:  make(map[string]string),
		State:   make(map[string]interface{}),
		Outputs: make(map[string]string),
		Events:  []Event{},
	}

	// Use bufio.Reader instead of Scanner - no line length limits
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// Process final line if no trailing newline
				if len(line) > 0 {
					if parseErr := s.parseJSONLLine(line, sess); parseErr != nil {
						return nil, parseErr
					}
				}
				break
			}
			return nil, fmt.Errorf("error reading JSONL: %w", err)
		}

		// Skip empty lines
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if err := s.parseJSONLLine(line, sess); err != nil {
			return nil, err
		}
	}

	// Restore sequence counter from last event
	if len(sess.Events) > 0 {
		sess.seqCounter = sess.Events[len(sess.Events)-1].SeqID
	}

	return sess, nil
}

// parseJSONLLine parses a single JSONL line into the session.
func (s *FileStore) parseJSONLLine(line []byte, sess *Session) error {
	var record JSONLRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return fmt.Errorf("failed to parse JSONL line: %w", err)
	}

	switch record.RecordType {
	case RecordTypeHeader:
		sess.ID = record.ID
		sess.WorkflowName = record.WorkflowName
		sess.Inputs = record.Inputs
		sess.CreatedAt = record.CreatedAt
		
	case RecordTypeEvent:
		if record.Event != nil {
			sess.Events = append(sess.Events, *record.Event)
		}
		
	case RecordTypeFooter:
		sess.Status = record.Status
		sess.Result = record.Result
		sess.Error = record.Error
		sess.Outputs = record.Outputs
		sess.State = record.State
		sess.UpdatedAt = record.UpdatedAt
	}

	return nil
}

// loadLegacyJSON loads a session from legacy JSON format.
func (s *FileStore) loadLegacyJSON(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}

	// Restore sequence counter from last event
	if len(sess.Events) > 0 {
		sess.seqCounter = sess.Events[len(sess.Events)-1].SeqID
	}

	return &sess, nil
}

// FileManager wraps FileStore to implement SessionManager.
type FileManager struct {
	store *FileStore
}

// NewFileManager creates a new file-based session manager.
func NewFileManager(dir string) SessionManager {
	store, err := NewFileStore(dir)
	if err != nil {
		// Fallback: create with error handling in actual use
		return &FileManager{store: &FileStore{dir: dir}}
	}
	return &FileManager{store: store}
}

// Create creates a new session.
func (m *FileManager) Create(workflowName string) (*Session, error) {
	id := generateID()
	now := time.Now()

	sess := &Session{
		ID:           id,
		WorkflowName: workflowName,
		Inputs:       make(map[string]string),
		State:        make(map[string]interface{}),
		Outputs:      make(map[string]string),
		Status:       StatusRunning,
		Events:       []Event{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := m.store.Save(sess); err != nil {
		return nil, err
	}

	return sess, nil
}

// Update updates a session.
func (m *FileManager) Update(sess *Session) error {
	sess.UpdatedAt = time.Now()
	return m.store.Save(sess)
}

// Get retrieves a session by ID.
func (m *FileManager) Get(id string) (*Session, error) {
	return m.store.Load(id)
}

// DetectFormat checks if a file is JSONL or legacy JSON format.
func DetectFormat(path string) (string, error) {
	// Check extension first
	if strings.HasSuffix(path, ".jsonl") {
		return "jsonl", nil
	}
	if strings.HasSuffix(path, ".json") {
		return "json", nil
	}
	
	// Peek at content
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil {
		return "", err
	}
	
	content := string(buf[:n])
	// JSONL header has _type field
	if strings.Contains(content, `"_type"`) {
		return "jsonl", nil
	}
	// Legacy JSON has events array
	if strings.Contains(content, `"events"`) {
		return "json", nil
	}
	
	return "json", nil // default to legacy
}
