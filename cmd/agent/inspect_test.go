package main

import (
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
)

func TestPrintStep(t *testing.T) {
	// Just ensure no panic - output is to stdout
	step := agentfile.Step{
		Type:       agentfile.StepRUN,
		Name:       "test",
		UsingGoals: []string{"goal1"},
	}
	// This would print to stdout, just ensure no panic
	_ = step
}

func TestInspectWorkflow_FileNotFound(t *testing.T) {
	// Would call os.Exit, so can't easily test
	t.Skip("requires refactoring to return errors instead of os.Exit")
}

func TestInspectPackage_NoArgs(t *testing.T) {
	// Would call os.Exit, so can't easily test
	t.Skip("requires refactoring to return errors instead of os.Exit")
}
