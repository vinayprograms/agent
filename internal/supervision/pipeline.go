package supervision

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
)

// EventHook receives supervision phase events (optional callback).
type EventHook func(stepID string, phase string, data any)

// PhaseLogger logs supervision phase timing and details.
// The executor's existing logPhaseReconcile / logPhaseSupervise methods
// satisfy this interface through a thin adapter.
type PhaseLogger interface {
	LogPhaseReconcile(goal, stepID string, triggers []string, escalate bool, durationMs int64)
	LogPhaseSupervise(goal, stepID, verdict, correction string, humanRequired bool, durationMs int64)
	LogCheckpoint(checkpointType, goal, step, checkpointID string)
}

// ExecuteResult is returned by the caller-supplied execute function.
type ExecuteResult struct {
	Output        string
	ToolsUsed     []string
	ToolCallsMade bool // only meaningful for goal execution
}

// Store is what the pipeline needs from a checkpoint store; *checkpoint.Store
// satisfies it. Implementations must be safe for concurrent use.
type Store interface {
	SavePre(*checkpoint.PreCheckpoint) error
	SavePost(*checkpoint.PostCheckpoint) error
	SaveReconcile(*checkpoint.ReconcileResult) error
	SaveSupervise(*checkpoint.SuperviseResult) error
	// Trail returns every checkpoint saved so far, in step order.
	Trail() []checkpoint.Checkpoint
}

// PipelineConfig configures a supervision pipeline instance.
type PipelineConfig struct {
	Store      Store
	Supervisor Supervisor
	Logger     *slog.Logger // warnings (store failures); nil means slog.Default()
	Phase      PhaseLogger  // phase-level session logging
	Event      EventHook    // optional event callback
}

// PipelineRequest contains the inputs for a single pipeline run.
type PipelineRequest struct {
	StepID        string // e.g. goal name or "subagent:<role>"
	GoalName      string // name of the goal this work belongs to (for logs)
	Outcome       string // the goal's description, for supervisor context
	Supervised    bool
	HumanRequired bool
}

// PipelineResult contains the outputs from a pipeline run.
type PipelineResult struct {
	Output        string
	ToolsUsed     []string
	ToolCallsMade bool

	// Supervision verdict outcomes
	Verdict    Verdict // VerdictContinue if no supervision ran
	Correction string  // non-empty for REORIENT
	Question   string  // non-empty for PAUSE
}

// Work is the caller-supplied half of a pipeline run: how to declare intent,
// do the work, and self-assess. These differ across goal types and
// sub-agents; the pipeline supplies the common save/log/event/reconcile/
// supervise flow around them.
type Work struct {
	// Commit declares intent before execution. A nil result skips supervision.
	Commit func(ctx context.Context) *checkpoint.PreCheckpoint
	// Execute performs the actual work.
	Execute func(ctx context.Context) (*ExecuteResult, error)
	// Post creates a self-assessment after execution. A nil result skips
	// reconcile and supervise.
	Post func(ctx context.Context, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint
}

// Pipeline manages the four-phase supervision flow:
// COMMIT -> EXECUTE -> RECONCILE -> SUPERVISE.
type Pipeline struct {
	cfg PipelineConfig
}

// NewPipeline creates a supervision pipeline. A nil cfg.Logger falls back to
// slog.Default().
func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Pipeline{cfg: cfg}
}

// Run executes the full supervision pipeline around work.
//
// If not supervised (or infrastructure is missing), it just calls work.Execute.
// If supervised, it runs: COMMIT -> EXECUTE -> RECONCILE -> (optionally) SUPERVISE.
//
// The caller is responsible for handling the verdict in PipelineResult
// (e.g., re-executing with a correction for REORIENT, or returning an error for PAUSE).
func (p *Pipeline) Run(ctx context.Context, req PipelineRequest, work Work) (*PipelineResult, error) {
	supervised := req.Supervised && p.cfg.Supervisor != nil && p.cfg.Store != nil

	// ============================================
	// PHASE 1: COMMIT - Agent declares intent
	// ============================================
	var pre *checkpoint.PreCheckpoint
	if supervised {
		pre = work.Commit(ctx)
		if pre != nil {
			if err := p.cfg.Store.SavePre(pre); err != nil {
				p.warn("failed to save pre-checkpoint", req.StepID, err)
			} else if p.cfg.Phase != nil {
				p.cfg.Phase.LogCheckpoint("pre", req.StepID, "", pre.StepID)
			}
			p.fireEvent(req.StepID, "commit", pre)
		}
	}

	// ============================================
	// PHASE 2: EXECUTE - Do the work
	// ============================================
	execResult, execErr := work.Execute(ctx)
	if execErr != nil {
		result := &PipelineResult{Verdict: VerdictContinue}
		if execResult != nil {
			result.Output = execResult.Output
			result.ToolsUsed = execResult.ToolsUsed
			result.ToolCallsMade = execResult.ToolCallsMade
		}
		return result, execErr
	}

	output := execResult.Output
	toolsUsed := execResult.ToolsUsed
	toolCallsMade := execResult.ToolCallsMade

	// Create post-checkpoint with self-assessment
	var post *checkpoint.PostCheckpoint
	if supervised && pre != nil {
		post = work.Post(ctx, pre, output, toolsUsed)
		if post != nil {
			if err := p.cfg.Store.SavePost(post); err != nil {
				p.warn("failed to save post-checkpoint", req.StepID, err)
			} else if p.cfg.Phase != nil {
				p.cfg.Phase.LogCheckpoint("post", req.StepID, "", post.StepID)
			}
			p.fireEvent(req.StepID, "execute", post)
		}
	}

	// ============================================
	// PHASE 3 & 4: RECONCILE & SUPERVISE
	// ============================================
	if !supervised || pre == nil || post == nil {
		return &PipelineResult{
			Output:        output,
			ToolsUsed:     toolsUsed,
			ToolCallsMade: toolCallsMade,
			Verdict:       VerdictContinue,
		}, nil
	}

	// RECONCILE: Static pattern checks
	reconcileStart := time.Now()
	reconcileResult := p.cfg.Supervisor.Reconcile(pre, post)
	reconcileDuration := time.Since(reconcileStart).Milliseconds()

	if err := p.cfg.Store.SaveReconcile(reconcileResult); err != nil {
		p.warn("failed to save reconcile result", req.StepID, err)
	}
	if p.cfg.Phase != nil {
		p.cfg.Phase.LogPhaseReconcile(req.StepID, reconcileResult.StepID, reconcileResult.Triggers, reconcileResult.Supervise, reconcileDuration)
	}
	p.fireEvent(req.StepID, "reconcile", reconcileResult)

	// SUPERVISE: LLM evaluation (only if reconcile triggered or human required)
	if !reconcileResult.Supervise && !req.HumanRequired {
		return &PipelineResult{
			Output:        output,
			ToolsUsed:     toolsUsed,
			ToolCallsMade: toolCallsMade,
			Verdict:       VerdictContinue,
		}, nil
	}

	superviseStart := time.Now()
	decisionTrail := p.cfg.Store.Trail()
	superviseResult, err := p.cfg.Supervisor.Supervise(
		ctx,
		SuperviseRequest{
			GoalName:      req.GoalName,
			Outcome:       req.Outcome,
			Pre:           pre,
			Post:          post,
			Triggers:      reconcileResult.Triggers,
			DecisionTrail: decisionTrail,
			HumanRequired: req.HumanRequired,
		},
	)
	superviseDuration := time.Since(superviseStart).Milliseconds()

	if err != nil {
		// Supervision failed -- return the execution output with the error
		return &PipelineResult{
			Output:        output,
			ToolsUsed:     toolsUsed,
			ToolCallsMade: toolCallsMade,
			Verdict:       VerdictContinue,
		}, fmt.Errorf("supervision failed: %w", err)
	}

	if err := p.cfg.Store.SaveSupervise(superviseResult); err != nil {
		p.warn("failed to save supervise result", req.StepID, err)
	}
	if p.cfg.Phase != nil {
		p.cfg.Phase.LogPhaseSupervise(req.StepID, superviseResult.StepID, superviseResult.Verdict, superviseResult.Correction, req.HumanRequired, superviseDuration)
		p.cfg.Phase.LogCheckpoint("supervise", req.StepID, superviseResult.StepID, superviseResult.StepID)
	}
	p.fireEvent(req.StepID, "supervise", superviseResult)

	verdict := Verdict(superviseResult.Verdict)
	return &PipelineResult{
		Output:        output,
		ToolsUsed:     toolsUsed,
		ToolCallsMade: toolCallsMade,
		Verdict:       verdict,
		Correction:    superviseResult.Correction,
		Question:      superviseResult.Question,
	}, nil
}

func (p *Pipeline) fireEvent(stepID, phase string, data any) {
	if p.cfg.Event != nil {
		p.cfg.Event(stepID, phase, data)
	}
}

func (p *Pipeline) warn(msg, step string, err error) {
	p.cfg.Logger.Warn(msg, "step", step, "error", err.Error())
}
