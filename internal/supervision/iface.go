// Package supervision detects drift and corrects agent execution via the
// four-phase model: COMMIT, EXECUTE, RECONCILE, SUPERVISE.
package supervision

import (
	"context"

	"github.com/vinayprograms/agent/internal/checkpoint"
)

// SuperviseRequest contains all inputs for the SUPERVISE phase.
type SuperviseRequest struct {
	OriginalGoal  string
	Pre           *checkpoint.PreCheckpoint
	Post          *checkpoint.PostCheckpoint
	Triggers      []string
	DecisionTrail []*checkpoint.Checkpoint
	HumanRequired bool
}

// Supervisor evaluates agent execution for drift and provides corrections.
type Supervisor interface {
	// Reconcile compares pre and post checkpoints to detect drift.
	// Returns a ReconcileResult with triggers for the SUPERVISE phase.
	Reconcile(pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult

	// Supervise evaluates drift and decides whether to continue, reorient, or pause.
	Supervise(ctx context.Context, req SuperviseRequest) (*checkpoint.SuperviseResult, error)
}
