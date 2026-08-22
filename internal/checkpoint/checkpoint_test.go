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

	cp, ok := store.Checkpoint("goal-001")
	if !ok {
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

	cp, _ := store.Checkpoint("goal-002")
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

	cp, _ := store.Checkpoint("goal-003")
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

	cp, _ := store.Checkpoint("goal-004")
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

	trail := store.Trail()
	if len(trail) != 3 || trail[0].Pre.StepID != "goal-001" || trail[2].Pre.StepID != "goal-003" {
		t.Errorf("Trail() = %v, want goal-001..goal-003 in order", trail)
	}
}

func TestOnDiskLayout(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store.SavePre(&PreCheckpoint{StepID: "goal-001", StepType: "GOAL", Interpretation: "Test interpretation", Timestamp: ts})
	store.SavePost(&PostCheckpoint{StepID: "goal-001", MetCommitment: true, Timestamp: ts})

	got, err := os.ReadFile(filepath.Join(dir, "goal-001.json"))
	if err != nil {
		t.Fatalf("reading checkpoint file: %v", err)
	}
	want := `{
  "pre": {
    "step_id": "goal-001",
    "step_type": "GOAL",
    "instruction": "",
    "interpretation": "Test interpretation",
    "approach": "",
    "predicted_output": "",
    "confidence": "",
    "timestamp": "2026-01-02T03:04:05Z"
  },
  "post": {
    "step_id": "goal-001",
    "actual_output": "",
    "met_commitment": true,
    "timestamp": "2026-01-02T03:04:05Z"
  }
}`
	if string(got) != want {
		t.Errorf("checkpoint file:\n%s\nwant:\n%s", got, want)
	}
}

func TestStepIDEscapedInFilename(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	if err := store.SavePre(&PreCheckpoint{StepID: "../escape/subagent:role"}); err != nil {
		t.Fatalf("SavePre: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "..%2Fescape%2Fsubagent%3Arole.json" {
		t.Errorf("files in dir = %v, want one escaped name", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape")); err == nil {
		t.Error("checkpoint escaped its directory")
	}
}

func TestCheckpointMissing(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	if cp, ok := store.Checkpoint("nope"); ok || cp != (Checkpoint{}) {
		t.Errorf("Checkpoint(nope) = %v, %v; want zero, false", cp, ok)
	}
}

func TestStoreErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := NewStore(filepath.Join(file, "sub")); err == nil {
		t.Error("NewStore under a file = nil error, want error")
	}

	dir := t.TempDir()
	store, _ := NewStore(dir)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if err := store.SavePre(&PreCheckpoint{StepID: "x"}); err == nil {
		t.Error("SavePre into read-only dir = nil error, want error")
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
	cp, _ := store.Checkpoint(stepID)
	if cp.Pre == nil || cp.Post == nil || cp.Reconcile == nil || cp.Supervise == nil {
		t.Error("incomplete checkpoint")
	}
	if cp.Supervise.Verdict != "CONTINUE" {
		t.Errorf("expected CONTINUE verdict, got %s", cp.Supervise.Verdict)
	}
}
