package security

import (
	"context"
	"fmt"
	"sync"

	"github.com/openclaw/headless-agent/internal/llm"
	"github.com/openclaw/headless-agent/internal/logging"
)

// Mode represents the security operation mode.
type Mode string

const (
	ModeDefault  Mode = "default"
	ModeParanoid Mode = "paranoid"
)

// Config holds security verifier configuration.
type Config struct {
	// Mode is the security mode (default or paranoid).
	Mode Mode

	// UserTrust is the trust level for user messages.
	UserTrust TrustLevel

	// TriageProvider is the LLM provider for Tier 2 triage (cheap/fast model).
	TriageProvider llm.Provider

	// SupervisorProvider is the LLM provider for Tier 3 supervision (capable model).
	SupervisorProvider llm.Provider

	// Logger for security events.
	Logger *logging.Logger
}

// Verifier implements the tiered security verification pipeline.
type Verifier struct {
	mode       Mode
	userTrust  TrustLevel
	triage     *Triage
	supervisor *SecuritySupervisor
	audit      *AuditTrail
	logger     *logging.Logger

	// blocks tracks content blocks in the current context
	blocks   []*Block
	blocksMu sync.RWMutex

	// blockCounter for generating unique IDs
	blockCounter int
}

// NewVerifier creates a new security verifier.
func NewVerifier(cfg Config, sessionID string) (*Verifier, error) {
	audit, err := NewAuditTrail(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit trail: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logging.New().WithComponent("security")
	}

	v := &Verifier{
		mode:      cfg.Mode,
		userTrust: cfg.UserTrust,
		audit:     audit,
		logger:    logger,
		blocks:    make([]*Block, 0),
	}

	if cfg.TriageProvider != nil {
		v.triage = NewTriage(cfg.TriageProvider)
	}

	if cfg.SupervisorProvider != nil {
		v.supervisor = NewSecuritySupervisor(cfg.SupervisorProvider)
	}

	logger.Info("security verifier initialized", map[string]interface{}{
		"mode":       string(cfg.Mode),
		"user_trust": string(cfg.UserTrust),
		"session_id": sessionID,
	})

	return v, nil
}

// AddBlock adds a content block to the context.
func (v *Verifier) AddBlock(trust TrustLevel, typ BlockType, mutable bool, content, source string) *Block {
	v.blocksMu.Lock()
	defer v.blocksMu.Unlock()

	v.blockCounter++
	id := fmt.Sprintf("b%04d", v.blockCounter)

	block := NewBlock(id, trust, typ, mutable, content, source)
	v.blocks = append(v.blocks, block)

	v.logger.Debug("block added", map[string]interface{}{
		"id":     block.ID,
		"trust":  string(block.Trust),
		"type":   string(block.Type),
		"source": source,
	})

	return block
}

// HighRiskTools is the set of tools that require extra scrutiny.
var HighRiskTools = map[string]bool{
	"bash":        true,
	"write":       true,
	"web_fetch":   true,
	"spawn_agent": true,
}

// VerifyToolCall runs the tiered verification pipeline for a tool call.
func (v *Verifier) VerifyToolCall(ctx context.Context, toolName string, args map[string]interface{}, originalGoal string) (*VerificationResult, error) {
	result := &VerificationResult{
		Allowed:  true,
		ToolName: toolName,
	}

	// Tier 1: Deterministic checks
	tier1Result := v.tier1Check(toolName, args)
	result.Tier1 = tier1Result

	if tier1Result.Pass {
		// No untrusted content or low-risk tool - allow
		v.recordDecision(tier1Result.Block, "pass", "skipped", "skipped")
		return result, nil
	}

	v.logger.Info("tier 1 escalated", map[string]interface{}{
		"tool":    toolName,
		"reasons": tier1Result.Reasons,
	})

	// Tier 2: Cheap model triage (skip in paranoid mode - go straight to T3)
	if v.mode != ModeParanoid && v.triage != nil {
		tier2Result, err := v.tier2Check(ctx, toolName, args, tier1Result.Block)
		if err != nil {
			v.logger.Error("tier 2 error", map[string]interface{}{"error": err.Error()})
			// Continue to tier 3 on error
		} else {
			result.Tier2 = tier2Result

			if !tier2Result.Suspicious {
				// Triage cleared
				v.recordDecision(tier1Result.Block, "escalate", "pass", "skipped")
				return result, nil
			}

			v.logger.Info("tier 2 escalated", map[string]interface{}{
				"tool":   toolName,
				"reason": tier2Result.Reason,
			})
		}
	}

	// Tier 3: Full supervisor
	if v.supervisor == nil {
		// No supervisor configured - fail-safe deny
		result.Allowed = false
		result.DenyReason = "no security supervisor configured, denying high-risk action"
		v.recordDecision(tier1Result.Block, "escalate", "escalate", "denied:no_supervisor")
		return result, nil
	}

	tier3Result, err := v.tier3Check(ctx, toolName, args, originalGoal, tier1Result)
	if err != nil {
		result.Allowed = false
		result.DenyReason = fmt.Sprintf("tier 3 error: %v", err)
		v.recordDecision(tier1Result.Block, "escalate", "escalate", "error")
		return result, nil
	}

	result.Tier3 = tier3Result
	tier3Log := string(tier3Result.Verdict)

	switch tier3Result.Verdict {
	case VerdictAllow:
		result.Allowed = true
	case VerdictDeny:
		result.Allowed = false
		result.DenyReason = tier3Result.Reason
	case VerdictModify:
		result.Allowed = false
		result.DenyReason = tier3Result.Reason
		result.Modification = tier3Result.Correction
	}

	v.recordDecision(tier1Result.Block, "escalate", "escalate", tier3Log)

	return result, nil
}

// Tier1Result holds the result of deterministic checks.
type Tier1Result struct {
	Pass    bool
	Reasons []string
	Block   *Block // The untrusted block that triggered escalation
}

func (v *Verifier) tier1Check(toolName string, args map[string]interface{}) *Tier1Result {
	result := &Tier1Result{Pass: true}

	// Check 1: Any untrusted content in context?
	untrustedBlocks := v.getUntrustedBlocks()
	if len(untrustedBlocks) == 0 {
		return result // No untrusted content - pass
	}

	// Check 2: Is this a high-risk tool?
	isHighRisk := HighRiskTools[toolName]
	if !isHighRisk {
		return result // Low-risk tool - pass with logging
	}

	result.Pass = false
	result.Reasons = append(result.Reasons, "high_risk_tool:"+toolName)

	// Check 3: Suspicious patterns in untrusted content
	for _, block := range untrustedBlocks {
		patterns := DetectSuspiciousPatterns(block.Content)
		for _, p := range patterns {
			result.Reasons = append(result.Reasons, "pattern:"+p.Name)
			result.Block = block
		}

		// Check 4: Encoded content
		if HasEncodedContent(block.Content) {
			result.Reasons = append(result.Reasons, "encoded_content")
			result.Block = block
		}
	}

	// Check args for suspicious patterns
	argsStr := fmt.Sprintf("%v", args)
	if HasSuspiciousPatterns(argsStr) {
		result.Reasons = append(result.Reasons, "suspicious_args")
	}

	if result.Block == nil && len(untrustedBlocks) > 0 {
		result.Block = untrustedBlocks[0]
	}

	return result
}

func (v *Verifier) tier2Check(ctx context.Context, toolName string, args map[string]interface{}, block *Block) (*TriageResult, error) {
	return v.triage.Evaluate(ctx, TriageRequest{
		ToolName:       toolName,
		ToolArgs:       args,
		UntrustedBlock: block,
	})
}

func (v *Verifier) tier3Check(ctx context.Context, toolName string, args map[string]interface{}, originalGoal string, tier1 *Tier1Result) (*SupervisionResult, error) {
	tier2Reason := "skipped"
	if v.mode == ModeParanoid {
		tier2Reason = "paranoid mode"
	}

	return v.supervisor.Evaluate(ctx, SupervisionRequest{
		ToolName:        toolName,
		ToolArgs:        args,
		UntrustedBlocks: v.getUntrustedBlocks(),
		OriginalGoal:    originalGoal,
		Tier1Flags:      tier1.Reasons,
		Tier2Reason:     tier2Reason,
	})
}

func (v *Verifier) getUntrustedBlocks() []*Block {
	v.blocksMu.RLock()
	defer v.blocksMu.RUnlock()

	var untrusted []*Block
	for _, b := range v.blocks {
		if b.Trust == TrustUntrusted {
			untrusted = append(untrusted, b)
		}
	}
	return untrusted
}

func (v *Verifier) recordDecision(block *Block, tier1, tier2, tier3 string) {
	if block == nil {
		return
	}
	v.audit.RecordDecision(block, tier1, tier2, tier3)
}

// VerificationResult holds the complete verification result.
type VerificationResult struct {
	Allowed      bool
	ToolName     string
	DenyReason   string
	Modification string
	Tier1        *Tier1Result
	Tier2        *TriageResult
	Tier3        *SupervisionResult
}

// AuditTrail returns the audit trail for export.
func (v *Verifier) AuditTrail() *AuditTrail {
	return v.audit
}

// Destroy cleans up resources, including zeroing the private key.
func (v *Verifier) Destroy() {
	if v.audit != nil {
		v.audit.Destroy()
	}
}

// ClearContext removes all blocks from the context.
func (v *Verifier) ClearContext() {
	v.blocksMu.Lock()
	defer v.blocksMu.Unlock()
	v.blocks = make([]*Block, 0)
}
