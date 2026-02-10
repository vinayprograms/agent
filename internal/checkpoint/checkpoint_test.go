package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if store == nil {
		t.Fatal("store is nil")
	}
}

func TestSaveAndGetPre(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	pre := &PreCheckpoint{
		StepID:          "goal-001",
		StepType:        "GOAL",
		Instruction:     "Find EV trends in Asia",
		Interpretation:  "Research 2024 EV adoption data",
		ScopeIn:         []string{"China", "Japan", "Korea"},
		ScopeOut:        []string{"Europe"},
		Approach:        "web_search, web_fetch, summarize",
		PredictedOutput: "Markdown report with trends",
		Confidence:      "high",
		Assumptions:     []string{"User wants recent data"},
		Timestamp:       time.Now(),
	}

	if err := store.SavePre(pre); err != nil {
		t.Fatalf("SavePre failed: %v", err)
	}

	cp := store.Get("goal-001")
	if cp == nil {
		t.Fatal("checkpoint not found")
	}
	if cp.Pre == nil {
		t.Fatal("pre-checkpoint is nil")
	}
	if cp.Pre.Interpretation != "Research 2024 EV adoption data" {
		t.Errorf("wrong interpretation: %s", cp.Pre.Interpretation)
	}

	// Verify file was written
	path := filepath.Join(dir, "goal-001.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("checkpoint file not written to disk")
	}
}

func TestSaveAndGetPost(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	// Save pre first
	pre := &PreCheckpoint{
		StepID:   "goal-002",
		StepType: "GOAL",
	}
	store.SavePre(pre)

	post := &PostCheckpoint{
		StepID:        "goal-002",
		ActualOutput:  "Report generated successfully",
		ToolsUsed:     []string{"web_search", "write"},
		MetCommitment: true,
		Deviations:    nil,
		Concerns:      nil,
		Timestamp:     time.Now(),
	}

	if err := store.SavePost(post); err != nil {
		t.Fatalf("SavePost failed: %v", err)
	}

	cp := store.Get("goal-002")
	if cp.Post == nil {
		t.Fatal("post-checkpoint is nil")
	}
	if !cp.Post.MetCommitment {
		t.Error("expected MetCommitment to be true")
	}
}

func TestSaveReconcile(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	rec := &ReconcileResult{
		StepID:    "goal-003",
		Triggers:  []string{"concerns_raised", "scope_deviation"},
		Supervise: true,
		Timestamp: time.Now(),
	}

	if err := store.SaveReconcile(rec); err != nil {
		t.Fatalf("SaveReconcile failed: %v", err)
	}

	cp := store.Get("goal-003")
	if cp.Reconcile == nil {
		t.Fatal("reconcile result is nil")
	}
	if !cp.Reconcile.Supervise {
		t.Error("expected Supervise to be true")
	}
	if len(cp.Reconcile.Triggers) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(cp.Reconcile.Triggers))
	}
}

func TestSaveSupervise(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	sup := &SuperviseResult{
		StepID:     "goal-004",
		Verdict:    "REORIENT",
		Correction: "Focus on consumer EVs, not commercial",
		Timestamp:  time.Now(),
	}

	if err := store.SaveSupervise(sup); err != nil {
		t.Fatalf("SaveSupervise failed: %v", err)
	}

	cp := store.Get("goal-004")
	if cp.Supervise == nil {
		t.Fatal("supervise result is nil")
	}
	if cp.Supervise.Verdict != "REORIENT" {
		t.Errorf("expected REORIENT, got %s", cp.Supervise.Verdict)
	}
}

func TestGetDecisionTrail(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	// Add multiple checkpoints
	store.SavePre(&PreCheckpoint{StepID: "goal-001", Interpretation: "First step"})
	store.SavePre(&PreCheckpoint{StepID: "goal-002", Interpretation: "Second step"})
	store.SavePre(&PreCheckpoint{StepID: "goal-003", Interpretation: "Third step"})

	trail := store.GetDecisionTrail()
	if len(trail) != 3 {
		t.Errorf("expected 3 checkpoints in trail, got %d", len(trail))
	}
}

func TestLoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	
	// Create and save with first store
	store1, _ := NewStore(dir)
	store1.SavePre(&PreCheckpoint{
		StepID:         "goal-001",
		Interpretation: "Test interpretation",
	})
	store1.SavePost(&PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: true,
	})

	// Create new store and load
	store2, _ := NewStore(dir)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cp := store2.Get("goal-001")
	if cp == nil {
		t.Fatal("checkpoint not loaded from disk")
	}
	if cp.Pre == nil || cp.Pre.Interpretation != "Test interpretation" {
		t.Error("pre-checkpoint not loaded correctly")
	}
	if cp.Post == nil || !cp.Post.MetCommitment {
		t.Error("post-checkpoint not loaded correctly")
	}
}

func TestCompleteCheckpointFlow(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	stepID := "goal-complete"

	// Phase 1: COMMIT
	pre := &PreCheckpoint{
		StepID:          stepID,
		StepType:        "GOAL",
		Instruction:     "Write a summary of AI trends",
		Interpretation:  "Create a brief overview of 2024 AI developments",
		Approach:        "Research, synthesize, write",
		PredictedOutput: "500-word summary",
		Confidence:      "medium",
		Assumptions:     []string{"Focus on LLMs", "Include safety topics"},
		Timestamp:       time.Now(),
	}
	store.SavePre(pre)

	// Phase 2: EXECUTE
	post := &PostCheckpoint{
		StepID:        stepID,
		ActualOutput:  "Generated 450-word summary",
		ToolsUsed:     []string{"web_search", "write"},
		MetCommitment: true,
		Concerns:      []string{"Limited sources available"},
		Timestamp:     time.Now(),
	}
	store.SavePost(post)

	// Phase 3: RECONCILE
	rec := &ReconcileResult{
		StepID:    stepID,
		Triggers:  []string{"concerns_raised"},
		Supervise: true,
		Timestamp: time.Now(),
	}
	store.SaveReconcile(rec)

	// Phase 4: SUPERVISE
	sup := &SuperviseResult{
		StepID:     stepID,
		Verdict:    "CONTINUE",
		Correction: "",
		Timestamp:  time.Now(),
	}
	store.SaveSupervise(sup)

	// Verify complete checkpoint
	cp := store.Get(stepID)
	if cp.Pre == nil || cp.Post == nil || cp.Reconcile == nil || cp.Supervise == nil {
		t.Error("incomplete checkpoint")
	}
	if cp.Supervise.Verdict != "CONTINUE" {
		t.Errorf("expected CONTINUE verdict, got %s", cp.Supervise.Verdict)
	}
}
