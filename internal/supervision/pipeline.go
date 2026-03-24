package supervision

import (
	"context"
	"fmt"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agentkit/logging"
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

// CommitResult is returned by the caller-supplied commit function.
type CommitResult struct {
	Pre *checkpoint.PreCheckpoint
}

// ExecuteResult is returned by the caller-supplied execute function.
type ExecuteResult struct {
	Output        string
	ToolsUsed     []string
	ToolCallsMade bool // only meaningful for goal execution
}

// PostCheckpointResult is returned by the caller-supplied post-checkpoint function.
type PostCheckpointResult struct {
	Post *checkpoint.PostCheckpoint
}

// PipelineConfig configures a supervision pipeline instance.
type PipelineConfig struct {
	Store      checkpoint.CheckpointStore
	Supervisor Supervisor
	Logger     *logging.Logger // structured logger (for warnings)
	Phase      PhaseLogger     // phase-level session logging
	OnEvent    EventHook       // optional event callback
}

// PipelineRequest contains the inputs for a single pipeline run.
type PipelineRequest struct {
	StepID        string // e.g. goal name or "subagent:<role>"
	GoalName      string // original goal description for supervisor context
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

// Pipeline manages the four-phase supervision flow:
// COMMIT -> EXECUTE -> RECONCILE -> SUPERVISE.
//
// The actual commit, execute, and post-checkpoint logic is provided by the
// caller as functions, since these differ across goal types and sub-agents.
// The pipeline handles the common save/log/event/reconcile/supervise flow.
type Pipeline struct {
	cfg PipelineConfig
}

// NewPipeline creates a supervision pipeline.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	return &Pipeline{cfg: cfg}
}

// CommitFunc declares intent before execution. Returns nil PreCheckpoint on failure.
type CommitFunc func(ctx context.Context) *checkpoint.PreCheckpoint

// ExecuteFunc performs the actual work.
type ExecuteFunc func(ctx context.Context) (*ExecuteResult, error)

// PostCheckpointFunc creates a self-assessment after execution.
type PostCheckpointFunc func(ctx context.Context, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint

// Run executes the full supervision pipeline around the given work functions.
//
// If not supervised (or infrastructure is missing), it just calls execute directly.
// If supervised, it runs: COMMIT -> EXECUTE -> RECONCILE -> (optionally) SUPERVISE.
//
// The caller is responsible for handling the verdict in PipelineResult
// (e.g., re-executing with a correction for REORIENT, or returning an error for PAUSE).
func (p *Pipeline) Run(
	ctx context.Context,
	req PipelineRequest,
	commit CommitFunc,
	execute ExecuteFunc,
	postCheckpoint PostCheckpointFunc,
) (*PipelineResult, error) {

	supervised := req.Supervised && p.cfg.Supervisor != nil && p.cfg.Store != nil

	// ============================================
	// PHASE 1: COMMIT - Agent declares intent
	// ============================================
	var pre *checkpoint.PreCheckpoint
	if supervised {
		pre = commit(ctx)
		if pre != nil {
			if err := p.cfg.Store.SavePre(pre); err != nil {
				p.warn("failed to save pre-checkpoint", map[string]any{
					"step":  req.StepID,
					"error": err.Error(),
				})
			} else if p.cfg.Phase != nil {
				p.cfg.Phase.LogCheckpoint("pre", req.StepID, "", pre.StepID)
			}
			p.fireEvent(req.StepID, "commit", pre)
		}
	}

	// ============================================
	// PHASE 2: EXECUTE - Do the work
	// ============================================
	execResult, execErr := execute(ctx)
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
		post = postCheckpoint(ctx, pre, output, toolsUsed)
		if post != nil {
			if err := p.cfg.Store.SavePost(post); err != nil {
				p.warn("failed to save post-checkpoint", map[string]any{
					"step":  req.StepID,
					"error": err.Error(),
				})
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
		p.warn("failed to save reconcile result", map[string]any{
			"step":  req.StepID,
			"error": err.Error(),
		})
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
	decisionTrail := p.cfg.Store.GetDecisionTrail()
	superviseResult, err := p.cfg.Supervisor.Supervise(
		ctx,
		SuperviseRequest{
			OriginalGoal:  req.GoalName,
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
		p.warn("failed to save supervise result", map[string]any{
			"step":  req.StepID,
			"error": err.Error(),
		})
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
	if p.cfg.OnEvent != nil {
		p.cfg.OnEvent(stepID, phase, data)
	}
}

func (p *Pipeline) warn(msg string, fields map[string]any) {
	if p.cfg.Logger != nil {
		p.cfg.Logger.Warn(msg, fields)
	}
}
