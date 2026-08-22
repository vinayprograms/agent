// Package checkpoint records execution phases for supervision, providing
// pre/post snapshots that enable drift detection and course correction.
package checkpoint

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	StepID    string    `json:"step_id"`
	Triggers  []string  `json:"triggers"`
	Supervise bool      `json:"supervise"`
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

// Store keeps one Checkpoint per step in memory and mirrors each to
// <dir>/<stepID>.json on every save. It is safe for concurrent use.
type Store struct {
	dir         string
	mu          sync.RWMutex
	checkpoints map[string]*Checkpoint
	order       []string // step IDs in first-seen order
}

// NewStore creates a checkpoint store writing under dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating checkpoint directory: %w", err)
	}
	return &Store{dir: dir, checkpoints: make(map[string]*Checkpoint)}, nil
}

// SavePre records the COMMIT phase of a step.
func (s *Store) SavePre(cp *PreCheckpoint) error {
	return s.upsert(cp.StepID, func(c *Checkpoint) { c.Pre = cp })
}

// SavePost records the EXECUTE phase of a step.
func (s *Store) SavePost(cp *PostCheckpoint) error {
	return s.upsert(cp.StepID, func(c *Checkpoint) { c.Post = cp })
}

// SaveReconcile records the RECONCILE phase of a step.
func (s *Store) SaveReconcile(r *ReconcileResult) error {
	return s.upsert(r.StepID, func(c *Checkpoint) { c.Reconcile = r })
}

// SaveSupervise records the SUPERVISE phase of a step.
func (s *Store) SaveSupervise(r *SuperviseResult) error {
	return s.upsert(r.StepID, func(c *Checkpoint) { c.Supervise = r })
}

// Checkpoint returns a copy of the checkpoint for stepID. The phase records
// inside it are the values passed to the Save* methods.
func (s *Store) Checkpoint(stepID string) (Checkpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp, ok := s.checkpoints[stepID]
	if !ok {
		return Checkpoint{}, false
	}
	return *cp, true
}

// Trail returns every checkpoint in the order its step was first saved.
func (s *Store) Trail() []Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trail := make([]Checkpoint, 0, len(s.order))
	for _, id := range s.order {
		trail = append(trail, *s.checkpoints[id])
	}
	return trail
}

// upsert applies set to the step's checkpoint (creating it on first sight)
// and flushes it to disk.
func (s *Store) upsert(stepID string, set func(*Checkpoint)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.checkpoints[stepID]
	if !ok {
		cp = &Checkpoint{}
		s.checkpoints[stepID] = cp
		s.order = append(s.order, stepID)
	}
	set(cp)

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding checkpoint %q: %w", stepID, err)
	}
	// Step IDs such as "subagent:role" or "a/b" must not steer the path.
	path := filepath.Join(s.dir, url.PathEscape(stepID)+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing checkpoint: %w", err)
	}
	return nil
}
