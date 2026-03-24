// Package checkpoint records execution phases for supervision, providing
// pre/post snapshots that enable drift detection and course correction.
package checkpoint

// CheckpointStore manages checkpoints for supervised execution.
// Implementations must be safe for concurrent use.
type CheckpointStore interface {
	// SavePre saves a pre-execution checkpoint (COMMIT phase).
	SavePre(cp *PreCheckpoint) error
	// SavePost saves a post-execution checkpoint (EXECUTE phase).
	SavePost(cp *PostCheckpoint) error
	// SaveReconcile saves a reconciliation result (RECONCILE phase).
	SaveReconcile(r *ReconcileResult) error
	// SaveSupervise saves a supervision result (SUPERVISE phase).
	SaveSupervise(r *SuperviseResult) error
	// Get returns the checkpoint for a given step ID, or nil if not found.
	Get(stepID string) *Checkpoint
	// GetDecisionTrail returns all checkpoints in chronological order.
	GetDecisionTrail() []*Checkpoint
}
