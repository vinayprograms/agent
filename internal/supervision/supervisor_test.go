package supervision

import (
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
)

func TestReconcile_NoTriggers(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "high",
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: true,
		// No concerns, deviations, or unexpected
	}

	result := sup.Reconcile(pre, post)

	if result.Supervise {
		t.Error("expected no supervision needed")
	}
	if len(result.Triggers) != 0 {
		t.Errorf("expected 0 triggers, got %d", len(result.Triggers))
	}
}

func TestReconcile_ConcernsRaised(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "high",
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: true,
		Concerns:      []string{"Data quality is questionable"},
	}

	result := sup.Reconcile(pre, post)

	if !result.Supervise {
		t.Error("expected supervision needed")
	}
	if len(result.Triggers) != 1 || result.Triggers[0] != string(TriggerConcernsRaised) {
		t.Errorf("expected concerns_raised trigger, got %v", result.Triggers)
	}
}

func TestReconcile_CommitmentNotMet(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "high",
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: false,
		Deviations:    []string{"Could not complete due to API error"},
	}

	result := sup.Reconcile(pre, post)

	if !result.Supervise {
		t.Error("expected supervision needed")
	}

	foundCommitmentTrigger := false
	foundDeviationTrigger := false
	for _, t := range result.Triggers {
		if t == string(TriggerCommitmentNotMet) {
			foundCommitmentTrigger = true
		}
		if t == string(TriggerScopeDeviation) {
			foundDeviationTrigger = true
		}
	}

	if !foundCommitmentTrigger {
		t.Error("expected commitment_not_met trigger")
	}
	if !foundDeviationTrigger {
		t.Error("expected scope_deviation trigger")
	}
}

func TestReconcile_LowConfidence(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "low",
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: true,
	}

	result := sup.Reconcile(pre, post)

	if !result.Supervise {
		t.Error("expected supervision needed for low confidence")
	}

	found := false
	for _, t := range result.Triggers {
		if t == string(TriggerLowConfidence) {
			found = true
		}
	}
	if !found {
		t.Error("expected low_confidence trigger")
	}
}

func TestReconcile_ExcessAssumptions(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "high",
		Assumptions: []string{
			"Assumption 1",
			"Assumption 2",
			"Assumption 3",
			"Assumption 4", // More than 3
		},
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: true,
	}

	result := sup.Reconcile(pre, post)

	if !result.Supervise {
		t.Error("expected supervision needed for excess assumptions")
	}

	found := false
	for _, t := range result.Triggers {
		if t == string(TriggerExcessAssumptions) {
			found = true
		}
	}
	if !found {
		t.Error("expected excess_assumptions trigger")
	}
}

func TestReconcile_MultipleTriggers(t *testing.T) {
	sup := New(Config{OriginalGoal: "Test goal"})

	pre := &checkpoint.PreCheckpoint{
		StepID:     "goal-001",
		Confidence: "low",
		Assumptions: []string{"A1", "A2", "A3", "A4", "A5"},
	}

	post := &checkpoint.PostCheckpoint{
		StepID:        "goal-001",
		MetCommitment: false,
		Concerns:      []string{"Multiple issues found"},
		Deviations:    []string{"Changed scope"},
		Unexpected:    []string{"Found unrelated data"},
	}

	result := sup.Reconcile(pre, post)

	if !result.Supervise {
		t.Error("expected supervision needed")
	}

	// Should have all triggers
	expectedTriggers := 6
	if len(result.Triggers) != expectedTriggers {
		t.Errorf("expected %d triggers, got %d: %v", expectedTriggers, len(result.Triggers), result.Triggers)
	}
}

func TestParseSupervisionResponse_Continue(t *testing.T) {
	sup := New(Config{})

	tests := []string{
		"CONTINUE",
		"continue",
		"CONTINUE: All good",
		"  CONTINUE  ",
	}

	for _, input := range tests {
		verdict, correction, question := sup.parseSupervisionResponse(input)
		if verdict != VerdictContinue {
			t.Errorf("input %q: expected CONTINUE, got %s", input, verdict)
		}
		if correction != "" || question != "" {
			t.Errorf("input %q: expected empty correction/question", input)
		}
	}
}

func TestParseSupervisionResponse_Reorient(t *testing.T) {
	sup := New(Config{})

	verdict, correction, _ := sup.parseSupervisionResponse(`REORIENT: Focus on consumer EVs only`)
	if verdict != VerdictReorient {
		t.Errorf("expected REORIENT, got %s", verdict)
	}
	if correction != "Focus on consumer EVs only" {
		t.Errorf("expected correction, got %q", correction)
	}

	// With quotes
	verdict2, correction2, _ := sup.parseSupervisionResponse(`REORIENT: "Include more sources"`)
	if verdict2 != VerdictReorient {
		t.Errorf("expected REORIENT, got %s", verdict2)
	}
	if correction2 != "Include more sources" {
		t.Errorf("expected correction without quotes, got %q", correction2)
	}
}

func TestParseSupervisionResponse_Pause(t *testing.T) {
	sup := New(Config{})

	verdict, _, question := sup.parseSupervisionResponse(`PAUSE: Should we include commercial vehicles?`)
	if verdict != VerdictPause {
		t.Errorf("expected PAUSE, got %s", verdict)
	}
	if question != "Should we include commercial vehicles?" {
		t.Errorf("expected question, got %q", question)
	}
}

func TestParseSupervisionResponse_DefaultToContinue(t *testing.T) {
	sup := New(Config{})

	// Unclear response should default to continue
	verdict, _, _ := sup.parseSupervisionResponse(`The agent seems to be doing fine.`)
	if verdict != VerdictContinue {
		t.Errorf("expected default CONTINUE, got %s", verdict)
	}
}

func TestSupervisor_SetOriginalGoal(t *testing.T) {
	sup := New(Config{OriginalGoal: "Initial goal"})

	if sup.originalGoal != "Initial goal" {
		t.Error("original goal not set")
	}

	sup.SetOriginalGoal("Updated goal")
	if sup.originalGoal != "Updated goal" {
		t.Error("original goal not updated")
	}
}

func TestSupervisor_SetHumanAvailable(t *testing.T) {
	sup := New(Config{HumanAvailable: false})

	if sup.humanAvailable {
		t.Error("expected human not available")
	}

	sup.SetHumanAvailable(true)
	if !sup.humanAvailable {
		t.Error("expected human available after set")
	}
}

func TestSupervisor_DefaultTimeout(t *testing.T) {
	sup := New(Config{})

	if sup.humanInputTimeout != 5*time.Minute {
		t.Errorf("expected 5 minute default timeout, got %v", sup.humanInputTimeout)
	}
}

func TestSupervisor_CustomTimeout(t *testing.T) {
	sup := New(Config{HumanInputTimeout: 10 * time.Second})

	if sup.humanInputTimeout != 10*time.Second {
		t.Errorf("expected 10 second timeout, got %v", sup.humanInputTimeout)
	}
}
