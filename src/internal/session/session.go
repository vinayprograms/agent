// Package session provides session management and persistence.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status constants for sessions.
const (
	StatusRunning  = "running"
	StatusComplete = "complete"
	StatusFailed   = "failed"
)

// Event types for the session log
const (
	EventSystem    = "system"    // System message to LLM
	EventUser      = "user"      // User/prompt message to LLM
	EventAssistant = "assistant" // LLM response
	EventToolCall  = "tool_call" // Tool invocation
	EventToolResult = "tool_result" // Tool result (fed back to LLM)
	EventGoalStart = "goal_start"
	EventGoalEnd   = "goal_end"
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
}

// Event represents a single entry in the session log.
// All events are in chronological order for easy reading.
type Event struct {
	Type      string                 `json:"type"`                // system, user, assistant, tool_call, tool_result, goal_start, goal_end
	Goal      string                 `json:"goal,omitempty"`      // Current goal context
	Content   string                 `json:"content,omitempty"`   // Message content or tool result
	Tool      string                 `json:"tool,omitempty"`      // Tool name (for tool_call/tool_result)
	Args      map[string]interface{} `json:"args,omitempty"`      // Tool arguments
	Error     string                 `json:"error,omitempty"`     // Error message if failed
	DurationMs int64                 `json:"duration_ms,omitempty"` // Tool execution time
	Timestamp time.Time              `json:"timestamp"`
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
		Status:       StatusRunning,
		Events:       []Event{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := m.store.Save(sess); err != nil {
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	return sess, nil
}

// Get retrieves a session by ID.
func (m *Manager) Get(id string) (*Session, error) {
	return m.store.Load(id)
}

// Complete marks a session as complete.
func (m *Manager) Complete(id string, result string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Load(id)
	if err != nil {
		return err
	}

	sess.Status = StatusComplete
	sess.Result = result
	sess.UpdatedAt = time.Now()

	return m.store.Save(sess)
}

// Fail marks a session as failed.
func (m *Manager) Fail(id string, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Load(id)
	if err != nil {
		return err
	}

	sess.Status = StatusFailed
	sess.Error = errMsg
	sess.UpdatedAt = time.Now()

	return m.store.Save(sess)
}

// UpdateState updates the session state.
func (m *Manager) UpdateState(id string, state map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Load(id)
	if err != nil {
		return err
	}

	sess.State = state
	sess.UpdatedAt = time.Now()

	return m.store.Save(sess)
}

// AddEvent adds an event to the session's chronological log.
func (m *Manager) AddEvent(id string, event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := m.store.Load(id)
	if err != nil {
		return err
	}

	sess.Events = append(sess.Events, event)
	sess.UpdatedAt = time.Now()

	return m.store.Save(sess)
}

// generateID generates a unique session ID.
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- FileStore ---

// FileStore stores sessions as JSON files.
type FileStore struct {
	dir string
}

// NewFileStore creates a new file store.
func NewFileStore(dir string) *FileStore {
	os.MkdirAll(dir, 0755)
	return &FileStore{dir: dir}
}

// Save saves a session to a JSON file.
func (s *FileStore) Save(sess *Session) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	filename := filepath.Join(s.dir, sess.ID+".json")
	tmpFile := filename + ".tmp"

	// Atomic write: write to temp file, then rename
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, filename); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// Load loads a session from a JSON file.
func (s *FileStore) Load(id string) (*Session, error) {
	filename := filepath.Join(s.dir, id+".json")

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", id)
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &sess, nil
}

// --- Interface for CLI compatibility ---

// SessionManager is the interface used by the CLI.
type SessionManager interface {
	Create(workflowName string) (*Session, error)
	Update(sess *Session) error
	Get(id string) (*Session, error)
}

// SimpleManager wraps a Store to implement SessionManager.
type SimpleManager struct {
	store Store
}

// NewFileManager creates a new file-based session manager.
func NewFileManager(path string) SessionManager {
	return &SimpleManager{store: NewFileStore(path)}
}

// Create creates a new session.
func (m *SimpleManager) Create(workflowName string) (*Session, error) {
	id := generateID()
	now := time.Now()

	sess := &Session{
		ID:           id,
		WorkflowName: workflowName,
		Inputs:       make(map[string]string),
		State:        make(map[string]interface{}),
		Outputs:      make(map[string]string),
		Status:       "running",
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
func (m *SimpleManager) Update(sess *Session) error {
	sess.UpdatedAt = time.Now()
	return m.store.Save(sess)
}

// Get retrieves a session by ID.
func (m *SimpleManager) Get(id string) (*Session, error) {
	return m.store.Load(id)
}
