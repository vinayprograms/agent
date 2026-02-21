// Package checkpoint provides checkpoint creation and management for supervised execution.
package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Phase represents the execution phase of a step.
type Phase string

const (
	PhaseCommit    Phase = "commit"
	PhaseExecute   Phase = "execute"
	PhaseReconcile Phase = "reconcile"
	PhaseSupervise Phase = "supervise"
)

// PreCheckpoint is created during the COMMIT phase before execution.
type PreCheckpoint struct {
	StepID          string            `json:"step_id"`
	StepType        string            `json:"step_type"` // GOAL, AGENT, RUN
	Instruction     string            `json:"instruction"`
	Interpretation  string            `json:"interpretation"`
	ScopeIn         []string          `json:"scope_in,omitempty"`
	ScopeOut        []string          `json:"scope_out,omitempty"`
	Approach        string            `json:"approach"`
	ToolsPlanned    []string          `json:"tools_planned,omitempty"`
	PredictedOutput string            `json:"predicted_output"`
	Confidence      string            `json:"confidence"` // high, medium, low
	Assumptions     []string          `json:"assumptions,omitempty"`
	Timestamp       time.Time         `json:"timestamp"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// PostCheckpoint is created during the EXECUTE phase after execution.
type PostCheckpoint struct {
	StepID        string    `json:"step_id"`
	ActualOutput  string    `json:"actual_output"`
	ToolsUsed     []string  `json:"tools_used,omitempty"`
	MetCommitment bool      `json:"met_commitment"`
	Deviations    []string  `json:"deviations,omitempty"`
	Concerns      []string  `json:"concerns,omitempty"`
	Unexpected    []string  `json:"unexpected,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// ReconcileResult is created during the RECONCILE phase.
type ReconcileResult struct {
	StepID    string   `json:"step_id"`
	Triggers  []string `json:"triggers"`
	Supervise bool     `json:"supervise"`
	Timestamp time.Time `json:"timestamp"`
}

// SuperviseResult is created during the SUPERVISE phase.
type SuperviseResult struct {
	StepID     string    `json:"step_id"`
	Verdict    string    `json:"verdict"` // CONTINUE, REORIENT, PAUSE
	Correction string    `json:"correction,omitempty"`
	Question   string    `json:"question,omitempty"` // for PAUSE
	Timestamp  time.Time `json:"timestamp"`
}

// Checkpoint represents a complete checkpoint for a step.
type Checkpoint struct {
	Pre       *PreCheckpoint   `json:"pre,omitempty"`
	Post      *PostCheckpoint  `json:"post,omitempty"`
	Reconcile *ReconcileResult `json:"reconcile,omitempty"`
	Supervise *SuperviseResult `json:"supervise,omitempty"`
}

// Decision represents a key decision made during execution.
type Decision struct {
	Choice string `json:"choice"`
	Reason string `json:"reason"`
}

// Store manages checkpoints for a session.
type Store struct {
	dir         string
	checkpoints map[string]*Checkpoint
	mu          sync.RWMutex
}

// NewStore creates a new checkpoint store.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint directory: %w", err)
	}
	return &Store{
		dir:         dir,
		checkpoints: make(map[string]*Checkpoint),
	}, nil
}

// SavePre saves a pre-checkpoint.
func (s *Store) SavePre(cp *PreCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.checkpoints[cp.StepID]; !ok {
		s.checkpoints[cp.StepID] = &Checkpoint{}
	}
	s.checkpoints[cp.StepID].Pre = cp

	return s.flush(cp.StepID)
}

// SavePost saves a post-checkpoint.
func (s *Store) SavePost(cp *PostCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.checkpoints[cp.StepID]; !ok {
		s.checkpoints[cp.StepID] = &Checkpoint{}
	}
	s.checkpoints[cp.StepID].Post = cp

	return s.flush(cp.StepID)
}

// SaveReconcile saves a reconcile result.
func (s *Store) SaveReconcile(r *ReconcileResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.checkpoints[r.StepID]; !ok {
		s.checkpoints[r.StepID] = &Checkpoint{}
	}
	s.checkpoints[r.StepID].Reconcile = r

	return s.flush(r.StepID)
}

// SaveSupervise saves a supervise result.
func (s *Store) SaveSupervise(r *SuperviseResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.checkpoints[r.StepID]; !ok {
		s.checkpoints[r.StepID] = &Checkpoint{}
	}
	s.checkpoints[r.StepID].Supervise = r

	return s.flush(r.StepID)
}

// Get retrieves a checkpoint by step ID.
func (s *Store) Get(stepID string) *Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.checkpoints[stepID]
}

// GetDecisionTrail returns all checkpoints in order for audit.
func (s *Store) GetDecisionTrail() []*Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var trail []*Checkpoint
	for _, cp := range s.checkpoints {
		trail = append(trail, cp)
	}
	return trail
}

// flush writes a checkpoint to disk.
func (s *Store) flush(stepID string) error {
	cp := s.checkpoints[stepID]
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(s.dir, fmt.Sprintf("%s.json", stepID))
	return os.WriteFile(path, data, 0644)
}

// Load loads checkpoints from disk.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var cp Checkpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			continue
		}

		// Extract step ID from filename
		stepID := entry.Name()[:len(entry.Name())-5] // remove .json
		s.checkpoints[stepID] = &cp
	}

	return nil
}
