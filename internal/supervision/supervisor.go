// Package supervision provides drift detection and course correction for agent execution.
package supervision

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/logging"
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

// Supervisor evaluates agent execution for drift and provides corrections.
type Supervisor struct {
	provider          llm.Provider
	logger            *logging.Logger
	originalGoal      string
	humanAvailable    bool
	humanInputChan    chan string
	humanInputTimeout time.Duration
}

// Config holds supervisor configuration.
type Config struct {
	Provider          llm.Provider
	OriginalGoal      string
	HumanAvailable    bool
	HumanInputChan    chan string
	HumanInputTimeout time.Duration
}

// New creates a new supervisor.
func New(cfg Config) *Supervisor {
	timeout := cfg.HumanInputTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	return &Supervisor{
		provider:          cfg.Provider,
		logger:            logging.New().WithComponent("supervisor"),
		originalGoal:      cfg.OriginalGoal,
		humanAvailable:    cfg.HumanAvailable,
		humanInputChan:    cfg.HumanInputChan,
		humanInputTimeout: timeout,
	}
}

// SetOriginalGoal updates the original goal for supervision.
func (s *Supervisor) SetOriginalGoal(goal string) {
	s.originalGoal = goal
}

// SetHumanAvailable updates whether a human is available.
func (s *Supervisor) SetHumanAvailable(available bool) {
	s.humanAvailable = available
}

// Reconcile performs static pattern checks on checkpoint data.
// Returns triggers that indicate need for supervision.
func (s *Supervisor) Reconcile(pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult {
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
	s.logger.ReconcilePhase("", pre.StepID, triggers, result.Supervise)
	s.logger.PhaseComplete("RECONCILE", "", pre.StepID, time.Since(start), fmt.Sprintf("supervise=%v", result.Supervise))

	return result
}

// Supervise evaluates the agent's work and decides whether to continue, reorient, or pause.
func (s *Supervisor) Supervise(ctx context.Context, pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint, triggers []string, decisionTrail []*checkpoint.Checkpoint, requiresHuman bool) (*checkpoint.SuperviseResult, error) {
	start := time.Now()
	s.logger.PhaseStart("SUPERVISE", "", pre.StepID)
	
	result := &checkpoint.SuperviseResult{
		StepID:    pre.StepID,
		Timestamp: time.Now(),
	}

	// Build prompt for supervisor
	prompt := s.buildSupervisionPrompt(pre, post, triggers, decisionTrail)

	messages := []llm.Message{
		{Role: "system", Content: supervisorSystemPrompt},
		{Role: "user", Content: prompt},
	}

	resp, err := s.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	if err != nil {
		s.logger.Error("supervisor_llm_error", map[string]interface{}{"error": err.Error()})
		return nil, fmt.Errorf("supervisor LLM error: %w", err)
	}

	// Parse response
	verdict, correction, question := s.parseSupervisionResponse(resp.Content)
	result.Verdict = string(verdict)
	result.Correction = correction
	result.Question = question

	// Log initial verdict
	s.logger.SupervisePhase("", pre.StepID, string(verdict), correction)

	// Handle PAUSE verdict
	if verdict == VerdictPause {
		if requiresHuman && !s.humanAvailable {
			// Hard fail - workflow requires human but none available
			s.logger.SupervisorVerdict("", pre.StepID, "PAUSE_FAILED", "human required but unavailable", true)
			return nil, fmt.Errorf("supervision requires human input but no human is available")
		}

		if s.humanAvailable && s.humanInputChan != nil {
			// Wait for human input
			s.logger.Info("waiting for human input", map[string]interface{}{
				"question": question,
				"timeout":  s.humanInputTimeout.String(),
			})

			select {
			case input := <-s.humanInputChan:
				// Human provided input, reorient with it
				result.Verdict = string(VerdictReorient)
				result.Correction = input
				s.logger.SupervisorVerdict("", pre.StepID, "REORIENT", "human provided input", true)
			case <-time.After(s.humanInputTimeout):
				if requiresHuman {
					s.logger.SupervisorVerdict("", pre.StepID, "PAUSE_TIMEOUT", "human input timeout", true)
					return nil, fmt.Errorf("human input timeout - workflow requires human approval")
				}
				// Timeout without required human - supervisor decides
				s.logger.Warn("human input timeout, supervisor will decide", nil)
				result.Verdict = string(VerdictContinue)
				result.Correction = "Proceeding without human input (timeout). Review output carefully."
				s.logger.SupervisorVerdict("", pre.StepID, "CONTINUE", "timeout fallback", false)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else if !requiresHuman {
			// No human available but not required - supervisor decides autonomously
			s.logger.Warn("no human available, supervisor deciding autonomously", nil)
			// Re-query with autonomous decision prompt
			autonomousResp, err := s.makeAutonomousDecision(ctx, pre, post, triggers, question)
			if err != nil {
				return nil, err
			}
			result.Verdict = string(autonomousResp.verdict)
			result.Correction = autonomousResp.correction
			s.logger.SupervisorVerdict("", pre.StepID, string(autonomousResp.verdict), "autonomous decision", false)
		}
	} else {
		// Log non-PAUSE verdicts
		s.logger.SupervisorVerdict("", pre.StepID, string(verdict), correction, false)
	}

	s.logger.PhaseComplete("SUPERVISE", "", pre.StepID, time.Since(start), result.Verdict)
	return result, nil
}

type autonomousDecision struct {
	verdict    Verdict
	correction string
}

func (s *Supervisor) makeAutonomousDecision(ctx context.Context, pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint, triggers []string, question string) (*autonomousDecision, error) {
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

	resp, err := s.provider.Chat(ctx, llm.ChatRequest{Messages: messages})
	if err != nil {
		return nil, err
	}

	// Parse simple response
	lines := strings.Split(resp.Content, "\n")
	decision := &autonomousDecision{verdict: VerdictContinue}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "VERDICT:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
			if strings.Contains(strings.ToUpper(v), "REORIENT") {
				decision.verdict = VerdictReorient
			}
		} else if strings.HasPrefix(line, "CORRECTION:") {
			decision.correction = strings.TrimSpace(strings.TrimPrefix(line, "CORRECTION:"))
		}
	}

	return decision, nil
}

func (s *Supervisor) buildSupervisionPrompt(pre *checkpoint.PreCheckpoint, post *checkpoint.PostCheckpoint, triggers []string, decisionTrail []*checkpoint.Checkpoint) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("ORIGINAL GOAL: %s\n\n", s.originalGoal))

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

func (s *Supervisor) parseSupervisionResponse(content string) (Verdict, string, string) {
	content = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "CONTINUE") {
			return VerdictContinue, "", ""
		}

		if strings.HasPrefix(upper, "REORIENT") {
			// Extract correction after colon
			if idx := strings.Index(line, ":"); idx != -1 {
				correction := strings.TrimSpace(line[idx+1:])
				correction = strings.Trim(correction, `"`)
				return VerdictReorient, correction, ""
			}
			return VerdictReorient, "", ""
		}

		if strings.HasPrefix(upper, "PAUSE") {
			// Extract question after colon
			if idx := strings.Index(line, ":"); idx != -1 {
				question := strings.TrimSpace(line[idx+1:])
				question = strings.Trim(question, `"`)
				return VerdictPause, "", question
			}
			return VerdictPause, "", ""
		}
	}

	// Default to continue if unclear
	return VerdictContinue, "", ""
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
