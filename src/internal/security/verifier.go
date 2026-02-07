package security

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/vinayprograms/agent/internal/llm"
	"github.com/vinayprograms/agent/internal/logging"
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
	return v.AddBlockWithContext(trust, typ, mutable, content, source, "")
}

// AddBlockWithContext adds a content block with an agent context identifier.
func (v *Verifier) AddBlockWithContext(trust TrustLevel, typ BlockType, mutable bool, content, source, agentContext string) *Block {
	v.blocksMu.Lock()
	defer v.blocksMu.Unlock()

	v.blockCounter++
	id := fmt.Sprintf("b%04d", v.blockCounter)

	block := NewBlock(id, trust, typ, mutable, content, source)
	block.AgentContext = agentContext
	v.blocks = append(v.blocks, block)

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
// agentContext filters blocks to only those from the same agent (empty = all blocks).
func (v *Verifier) VerifyToolCall(ctx context.Context, toolName string, args map[string]interface{}, originalGoal, agentContext string) (*VerificationResult, error) {
	result := &VerificationResult{
		Allowed:  true,
		ToolName: toolName,
	}

	// Tier 1: Deterministic checks
	tier1Result := v.tier1Check(toolName, args, agentContext)
	result.Tier1 = tier1Result

	if tier1Result.Pass {
		// No untrusted content or low-risk tool - allow
		v.recordDecision(tier1Result.Block, "pass", "skipped", "skipped")
		return result, nil
	}

	// Tier 2: Cheap model triage (skip in paranoid mode - go straight to T3)
	if v.mode != ModeParanoid && v.triage != nil {
		tier2Result, err := v.tier2Check(ctx, toolName, args, tier1Result.Block)
		if err == nil {
			result.Tier2 = tier2Result

			if !tier2Result.Suspicious {
				// Triage cleared
				v.recordDecision(tier1Result.Block, "escalate", "pass", "skipped")
				return result, nil
			}
		}
		// Continue to tier 3 on error
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
	Pass          bool
	Reasons       []string
	SkipReason    string   // Why escalation was skipped (for forensic clarity)
	Block         *Block   // The primary untrusted block that triggered escalation
	RelatedBlocks []*Block // All blocks whose content is used in this tool call
}

func (v *Verifier) tier1Check(toolName string, args map[string]interface{}, agentContext string) *Tier1Result {
	result := &Tier1Result{Pass: true}

	// Check 1: Any untrusted content in context?
	// Filter by agent context if specified
	untrustedBlocks := v.getUntrustedBlocksForContext(agentContext)
	if len(untrustedBlocks) == 0 {
		result.SkipReason = "no_untrusted_content"
		return result // No untrusted content - pass
	}

	// Check 2: Is this a high-risk tool?
	isHighRisk := HighRiskTools[toolName]
	if !isHighRisk {
		result.SkipReason = fmt.Sprintf("low_risk_tool:%s", toolName)
		return result // Low-risk tool - pass
	}

	result.Pass = false
	result.Reasons = append(result.Reasons, "high_risk_tool:"+toolName)

	// Serialize args for pattern matching
	argsStr := fmt.Sprintf("%v", args)

	// Check 3: Find ALL blocks whose content is being used in this tool call
	// This is simple taint tracking - check if args contain data from any block
	var relevantBlocks []*Block
	for _, block := range untrustedBlocks {
		if v.argsContainBlockData(argsStr, block) {
			relevantBlocks = append(relevantBlocks, block)
		}
	}

	// Check 4: Suspicious patterns in relevant blocks (or all if none found)
	blocksToCheck := untrustedBlocks
	if len(relevantBlocks) > 0 {
		blocksToCheck = relevantBlocks
		result.Block = relevantBlocks[0] // Primary block for reporting
		result.RelatedBlocks = relevantBlocks // All contributing blocks
	}

	// Collect pattern findings but don't let patterns override block selection
	// The block should be selected based on content correlation, not pattern presence
	for _, block := range blocksToCheck {
		patterns := DetectSuspiciousPatterns(block.Content)
		for _, p := range patterns {
			result.Reasons = append(result.Reasons, "pattern:"+p.Name)
		}

		// Check 5: Encoded content
		if HasEncodedContent(block.Content) {
			result.Reasons = append(result.Reasons, "encoded_content")
		}
	}

	// Check args for suspicious patterns
	if HasSuspiciousPatterns(argsStr) {
		result.Reasons = append(result.Reasons, "suspicious_args")
	}

	// If still no block identified, use the most recent one (not the first)
	if result.Block == nil && len(untrustedBlocks) > 0 {
		result.Block = untrustedBlocks[len(untrustedBlocks)-1]
	}

	return result
}

// argsContainBlockData checks if tool arguments contain data from a block.
// This is a simple substring match - full taint tracking would be more sophisticated.
func (v *Verifier) argsContainBlockData(argsStr string, block *Block) bool {
	// Extract meaningful substrings from block content to check
	// For URLs, check if any URL from the block appears in args
	urls := extractURLs(block.Content)
	for _, url := range urls {
		if len(url) > 20 && containsIgnoreCase(argsStr, url) {
			return true
		}
	}

	// For other content, check if significant portions appear
	// (Skip very short content to avoid false positives)
	if len(block.Content) > 100 {
		// Check if a meaningful chunk of block content appears in args
		// Use a sliding window of 50 chars
		for i := 0; i+50 <= len(block.Content) && i < 500; i += 25 {
			chunk := block.Content[i : i+50]
			if containsIgnoreCase(argsStr, chunk) {
				return true
			}
		}
	}

	return false
}

// extractURLs extracts URLs from content.
func extractURLs(content string) []string {
	var urls []string
	// Simple URL extraction - look for http:// or https://
	words := strings.Fields(content)
	for _, word := range words {
		// Clean up common JSON/markdown artifacts
		word = strings.Trim(word, `"',[]{}()`)
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			// Truncate at common terminators
			for _, term := range []string{`"`, `'`, `\u003c`, `>`, ` `, `\n`} {
				if idx := strings.Index(word, term); idx > 0 {
					word = word[:idx]
				}
			}
			urls = append(urls, word)
		}
	}
	return urls
}

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
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
	return v.getUntrustedBlocksForContext("")
}

// getUntrustedBlocksForContext returns untrusted blocks filtered by agent context.
// If agentContext is empty, returns all untrusted blocks.
// If agentContext is set, returns blocks matching that context OR blocks with no context (shared blocks).
func (v *Verifier) getUntrustedBlocksForContext(agentContext string) []*Block {
	v.blocksMu.RLock()
	defer v.blocksMu.RUnlock()

	var untrusted []*Block
	for _, b := range v.blocks {
		if b.Trust != TrustUntrusted {
			continue
		}
		// If no filter, include all
		if agentContext == "" {
			untrusted = append(untrusted, b)
			continue
		}
		// Include if block matches context OR has no context (shared)
		if b.AgentContext == agentContext || b.AgentContext == "" {
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
