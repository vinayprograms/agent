package supervision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agentkit/llm"
)

// Verdict represents the supervisor's decision.
type Verdict string

const (
	VerdictContinue Verdict = "CONTINUE"
	VerdictReorient Verdict = "REORIENT"
	VerdictPause    Verdict = "PAUSE"
)

// Trigger represents a reconciliation trigger.
type Trigger string

const (
	TriggerConcernsRaised    Trigger = "concerns_raised"
	TriggerCommitmentNotMet  Trigger = "commitment_not_met"
	TriggerScopeDeviation    Trigger = "scope_deviation"
	TriggerUnexpectedResults Trigger = "unexpected_results"
	TriggerLowConfidence     Trigger = "low_confidence"
	TriggerExcessAssumptions Trigger = "excess_assumptions"
)

// LLMSupervisor evaluates agent execution for drift and provides corrections
// using an LLM model.
type LLMSupervisor struct {
	model             llm.Model
	logger            *slog.Logger
	humanAvailable    bool
	humanInputChan    <-chan string
	humanInputTimeout time.Duration
}

// Verify LLMSupervisor implements Supervisor.
var _ Supervisor = (*LLMSupervisor)(nil)

// Config holds supervisor configuration.
type Config struct {
	// Model answers the supervision prompts. Required for Supervise; Reconcile
	// never calls it.
	Model llm.Model
	// Logger receives phase and verdict events. nil means slog.Default(), so a
	// zero Config logs like the old kit logger did (to wherever the process's
	// default handler points). The "component=supervisor" attribute is added
	// here.
	Logger *slog.Logger
	// HumanAvailable says a human can answer PAUSE questions; answers arrive
	// on HumanInputChan. With HumanAvailable and a nil channel, PAUSE verdicts
	// are returned as-is.
	HumanAvailable    bool
	HumanInputChan    <-chan string
	HumanInputTimeout time.Duration // zero means 5 minutes
}

// NewLLMSupervisor creates a new LLM-based supervisor.
func NewLLMSupervisor(cfg Config) *LLMSupervisor {
	timeout := cfg.HumanInputTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &LLMSupervisor{
		model:             cfg.Model,
		logger:            logger.With("component", "supervisor"),
		humanAvailable:    cfg.HumanAvailable,
		humanInputChan:    cfg.HumanInputChan,
		humanInputTimeout: timeout,
	}
}

// Reconcile performs static pattern checks on checkpoint data.
// Returns triggers that indicate need for supervision.
func (s *LLMSupervisor) Reconcile(pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult {
	start := time.Now()
	result := &checkpoint.ReconcileResult{
		StepID:    pre.StepID,
		Timestamp: time.Now(),
	}

	var triggers []string

	// Check: concerns raised
	if len(post.Concerns) > 0 {
		triggers = append(triggers, string(TriggerConcernsRaised))
	}

	// Check: commitment not met
	if !post.MetCommitment {
		triggers = append(triggers, string(TriggerCommitmentNotMet))
	}

	// Check: scope deviation
	if len(post.Deviations) > 0 {
		triggers = append(triggers, string(TriggerScopeDeviation))
	}

	// Check: unexpected results
	if len(post.Unexpected) > 0 {
		triggers = append(triggers, string(TriggerUnexpectedResults))
	}

	// Check: low confidence
	if pre.Confidence == "low" {
		triggers = append(triggers, string(TriggerLowConfidence))
	}

	// Check: too many assumptions (more than 3 is risky)
	if len(pre.Assumptions) > 3 {
		triggers = append(triggers, string(TriggerExcessAssumptions))
	}

	result.Triggers = triggers
	result.Supervise = len(triggers) > 0

	// Forensic logging
	s.logger.Debug("reconcile_phase",
		"goal", "",
		"step", pre.StepID,
		"triggers", strings.Join(triggers, ","),
		"escalate", result.Supervise)
	s.logPhaseComplete("RECONCILE", "", pre.StepID, start, fmt.Sprintf("supervise=%v", result.Supervise))

	return result
}

// Supervise evaluates the agent's work and decides whether to continue, reorient, or pause.
func (s *LLMSupervisor) Supervise(ctx context.Context, req SuperviseRequest) (*checkpoint.SuperviseResult, error) {
	pre := req.Pre
	post := req.Post
	triggers := req.Triggers
	decisionTrail := req.DecisionTrail
	requiresHuman := req.HumanRequired

	start := time.Now()
	s.logger.Debug("phase_start", "phase", "SUPERVISE", "goal", req.GoalName, "step", pre.StepID)

	result := &checkpoint.SuperviseResult{
		StepID:    pre.StepID,
		Timestamp: time.Now(),
	}

	// Build prompt for supervisor
	prompt := s.buildSupervisionPrompt(req.Outcome, pre, post, triggers, decisionTrail)

	messages := []llm.Message{
		{Role: "system", Content: supervisorSystemPrompt},
		{Role: "user", Content: prompt},
	}

	resp, err := s.model.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	if err != nil {
		return nil, fmt.Errorf("supervisor LLM error: %w", err)
	}

	d := parseSupervisionResponse(resp.Content)
	result.Verdict = string(d.verdict)
	result.Correction = d.correction
	result.Question = d.question

	// Log initial verdict
	s.logger.Debug("supervise_phase",
		"goal", "",
		"step", pre.StepID,
		"verdict", string(d.verdict),
		"reason", d.correction)

	// Handle PAUSE verdict
	if d.verdict == VerdictPause {
		if requiresHuman && !s.humanAvailable {
			// Hard fail - workflow requires human but none available
			s.logVerdict(req.GoalName, pre.StepID, "PAUSE_FAILED", "human required but unavailable", true)
			return nil, errors.New("supervision requires human input but no human is available")
		}

		if s.humanAvailable && s.humanInputChan != nil {
			// Wait for human input
			s.logger.Info("waiting for human input",
				"question", d.question,
				"timeout", s.humanInputTimeout.String())

			select {
			case input := <-s.humanInputChan:
				// Human provided input, reorient with it
				result.Verdict = string(VerdictReorient)
				result.Correction = input
				s.logVerdict(req.GoalName, pre.StepID, "REORIENT", "human provided input", true)
			case <-time.After(s.humanInputTimeout):
				if requiresHuman {
					s.logVerdict(req.GoalName, pre.StepID, "PAUSE_TIMEOUT", "human input timeout", true)
					return nil, errors.New("human input timeout - workflow requires human approval")
				}
				// Timeout without required human - supervisor decides
				s.logger.Warn("human input timeout, supervisor will decide")
				result.Verdict = string(VerdictContinue)
				result.Correction = "Proceeding without human input (timeout). Review output carefully."
				s.logVerdict(req.GoalName, pre.StepID, "CONTINUE", "timeout fallback", false)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else if !requiresHuman {
			// No human available but not required - supervisor decides autonomously
			s.logger.Warn("no human available, supervisor deciding autonomously")
			// Re-query with autonomous decision prompt
			auto, err := s.makeAutonomousDecision(ctx, d.question)
			if err != nil {
				return nil, err
			}
			result.Verdict = string(auto.verdict)
			result.Correction = auto.correction
			s.logVerdict(req.GoalName, pre.StepID, string(auto.verdict), "autonomous decision", false)
		}
	} else {
		// Log non-PAUSE verdicts
		s.logVerdict(req.GoalName, pre.StepID, string(d.verdict), d.correction, false)
	}

	s.logPhaseComplete("SUPERVISE", req.GoalName, pre.StepID, start, result.Verdict)
	return result, nil
}

// logVerdict mirrors the old kit's SupervisorVerdict event (same keys).
func (s *LLMSupervisor) logVerdict(goal, step, verdict, guidance string, humanRequired bool) {
	s.logger.Info("supervisor_verdict",
		"goal", goal,
		"step", step,
		"verdict", verdict,
		"guidance", guidance,
		"human_required", humanRequired)
}

// logPhaseComplete mirrors the old kit's PhaseComplete event (same keys).
func (s *LLMSupervisor) logPhaseComplete(phase, goal, step string, start time.Time, result string) {
	s.logger.Debug("phase_complete",
		"phase", phase,
		"goal", goal,
		"step", step,
		"duration", time.Since(start).String(),
		"result", result)
}

// decision is a parsed supervisor reply.
type decision struct {
	verdict    Verdict
	correction string // REORIENT guidance
	question   string // PAUSE question
}

// makeAutonomousDecision re-asks the model when it wanted a human (PAUSE)
// but none is available; the reply is constrained to CONTINUE or REORIENT.
func (s *LLMSupervisor) makeAutonomousDecision(ctx context.Context, question string) (decision, error) {
	prompt := fmt.Sprintf(`You previously wanted to ask: %s

But no human is available. You must make a decision autonomously.

Choose the most conservative safe path forward. If the deviation is minor, CONTINUE with a note.
If the deviation is significant but recoverable, REORIENT with specific guidance.

Respond with:
VERDICT: CONTINUE or REORIENT
CORRECTION: <your guidance>`, question)

	messages := []llm.Message{
		{Role: "system", Content: "You are making an autonomous decision because no human is available."},
		{Role: "user", Content: prompt},
	}

	resp, err := s.model.Chat(ctx, llm.ChatRequest{Messages: messages})
	if err != nil {
		return decision{}, err
	}

	d := decision{verdict: VerdictContinue}
	for line := range strings.Lines(resp.Content) {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "VERDICT:"); ok {
			if strings.Contains(strings.ToUpper(v), "REORIENT") {
				d.verdict = VerdictReorient
			}
		} else if c, ok := strings.CutPrefix(line, "CORRECTION:"); ok {
			d.correction = strings.TrimSpace(c)
		}
	}
	return d, nil
}

func (s *LLMSupervisor) buildSupervisionPrompt(originalGoal string, pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint, triggers []string, decisionTrail []checkpoint.Checkpoint) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("ORIGINAL GOAL: %s\n\n", originalGoal))

	sb.WriteString("AGENT COMMITTED TO:\n")
	sb.WriteString(fmt.Sprintf("- Interpretation: %s\n", pre.Interpretation))
	sb.WriteString(fmt.Sprintf("- Approach: %s\n", pre.Approach))
	sb.WriteString(fmt.Sprintf("- Predicted output: %s\n", pre.PredictedOutput))
	sb.WriteString(fmt.Sprintf("- Confidence: %s\n", pre.Confidence))
	if len(pre.ScopeOut) > 0 {
		sb.WriteString(fmt.Sprintf("- Excluded from scope: %s\n", strings.Join(pre.ScopeOut, ", ")))
	}
	if len(pre.Assumptions) > 0 {
		sb.WriteString(fmt.Sprintf("- Assumptions: %s\n", strings.Join(pre.Assumptions, "; ")))
	}
	sb.WriteString("\n")

	sb.WriteString("AGENT REPORTED:\n")
	sb.WriteString(fmt.Sprintf("- Met commitment: %v\n", post.MetCommitment))
	if len(post.Deviations) > 0 {
		sb.WriteString(fmt.Sprintf("- Deviations: %s\n", strings.Join(post.Deviations, "; ")))
	}
	if len(post.Concerns) > 0 {
		sb.WriteString(fmt.Sprintf("- Concerns: %s\n", strings.Join(post.Concerns, "; ")))
	}
	if len(post.Unexpected) > 0 {
		sb.WriteString(fmt.Sprintf("- Unexpected: %s\n", strings.Join(post.Unexpected, "; ")))
	}
	sb.WriteString(fmt.Sprintf("- Tools used: %s\n", strings.Join(post.ToolsUsed, ", ")))
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("TRIGGERED BY: %s\n\n", strings.Join(triggers, ", ")))

	if len(decisionTrail) > 0 {
		sb.WriteString("DECISION TRAIL:\n")
		for _, cp := range decisionTrail {
			if cp.Pre != nil {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", cp.Pre.StepID, cp.Pre.Interpretation))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`Evaluate:
1. Is the agent's interpretation faithful to the original goal?
2. Are the scope exclusions justified?
3. Do the deviations indicate drift?
4. Should execution continue, be corrected, or paused for human input?

Respond with ONE of:
- CONTINUE: Deviation is acceptable, proceed
- REORIENT: Course correct with guidance: "<correction>"
- PAUSE: Need human input: "<question>"`)

	return sb.String()
}

// parseSupervisionResponse finds the first line starting with a verdict;
// the text after the verdict's colon (quotes stripped) is the correction for
// REORIENT or the question for PAUSE. An unclear reply means CONTINUE.
func parseSupervisionResponse(content string) decision {
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		_, rest, hasColon := strings.Cut(line, ":")
		if hasColon {
			rest = strings.Trim(strings.TrimSpace(rest), `"`)
		}
		switch {
		case strings.HasPrefix(upper, "CONTINUE"):
			return decision{verdict: VerdictContinue}
		case strings.HasPrefix(upper, "REORIENT"):
			return decision{verdict: VerdictReorient, correction: rest}
		case strings.HasPrefix(upper, "PAUSE"):
			return decision{verdict: VerdictPause, question: rest}
		}
	}
	return decision{verdict: VerdictContinue}
}

const supervisorSystemPrompt = `You are a supervision agent reviewing another agent's work for alignment with the original goal.

Your job is to detect drift - when the agent's understanding or execution diverges from what the user actually wanted.

Be pragmatic:
- Minor deviations that don't affect the outcome are acceptable
- Reasonable assumptions under uncertainty are fine
- Only flag issues that materially affect the goal

Be conservative:
- When in doubt, ask for human input (PAUSE)
- Significant scope changes should be confirmed
- Accumulated assumptions are a red flag

Respond with exactly one verdict:
- CONTINUE: Work is aligned, proceed
- REORIENT: Work is drifting, provide correction guidance
- PAUSE: Uncertain, need human to clarify`
