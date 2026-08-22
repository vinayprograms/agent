package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agentkit/contentguard"
	"github.com/vinayprograms/agentkit/llm"
)

// verifiedTools are the tools whose calls are checked against untrusted
// content. Every other registered tool is placed on the guard's skip list.
// This mirrors the old verifier's high-risk set plus the destructive
// filesystem tools (ledger A-G8).
var verifiedTools = map[string]bool{
	"bash":         true,
	"write":        true,
	"edit":         true,
	"web_fetch":    true,
	"spawn_agents": true,
	"rm":           true,
	"mv":           true,
	"patch":        true,
}

// Stage source names as reported by contentguard findings.
const (
	stageDeterministic = "deterministic"
	stageScreener      = "screener"
	stageReviewer      = "reviewer"
)

// newContentGuard builds the verification pipeline for cfg. The security
// models are wrapped so that per-call token usage reaches the session log.
func newContentGuard(cfg *SecurityConfig, registered []string) (*contentguard.Guard, error) {
	var stages []contentguard.Stage
	if cfg.Screener != nil {
		stages = append(stages, contentguard.NewScreener(countingModel{inner: cfg.Screener, stage: stageScreener}))
	}
	if cfg.Reviewer != nil {
		stages = append(stages, contentguard.NewReviewer(countingModel{inner: cfg.Reviewer, stage: stageReviewer}))
	}

	var workflow contentguard.Workflow = contentguard.Escalatory()
	if cfg.Mode == SecurityParanoid {
		workflow = contentguard.Paranoid()
	}

	gcfg := contentguard.Config{
		Patterns: cfg.Patterns,
		Keywords: cfg.Keywords,
	}
	if cfg.Mode == SecurityResearch && cfg.Scope != "" {
		gcfg.Context = map[string]string{"scope": cfg.Scope}
	}
	for _, name := range registered {
		if !verifiedTools[name] {
			gcfg.Skip = append(gcfg.Skip, name)
		}
	}

	return contentguard.New(stages, workflow, gcfg)
}

// tokenUsage accumulates the security models' token counts for one Check.
// Safe for concurrent use.
type tokenUsage struct {
	mu      sync.Mutex
	in, out map[string]int
}

func newTokenUsage() *tokenUsage {
	return &tokenUsage{in: map[string]int{}, out: map[string]int{}}
}

func (u *tokenUsage) add(stage string, in, out int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.in[stage] += in
	u.out[stage] += out
}

func (u *tokenUsage) get(stage string) (in, out int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.in[stage], u.out[stage]
}

type tokenUsageKey struct{}

func withTokenUsage(ctx context.Context, u *tokenUsage) context.Context {
	return context.WithValue(ctx, tokenUsageKey{}, u)
}

// countingModel records each response's token counts into the tokenUsage
// carried by ctx (if any). contentguard discards them otherwise (A-G7).
type countingModel struct {
	inner llm.Model
	stage string
}

func (m countingModel) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	resp, err := m.inner.Chat(ctx, req)
	if resp != nil {
		if u, ok := ctx.Value(tokenUsageKey{}).(*tokenUsage); ok {
			u.add(m.stage, resp.InputTokens, resp.OutputTokens)
		}
	}
	return resp, err
}

// verifyToolCall checks a tool call against the content guard if configured.
// It returns the IDs of the untrusted blocks whose content the call used, so
// the call's result can be tainted by them.
func (e *Executor) verifyToolCall(ctx context.Context, toolName string, args map[string]any) ([]string, error) {
	if e.guard == nil {
		return nil, nil // No security verification configured
	}

	usage := newTokenUsage()
	result, err := e.guard.Check(withTokenUsage(ctx, usage), toolName, args, e.currentGoal)
	if err != nil {
		return nil, fmt.Errorf("security verification error: %w", err)
	}

	det := result.Findings[0]
	pass := det.Verdict == contentguard.Allow

	// Correlate args with untrusted blocks (taint tracking). Only escalated
	// calls get a primary block; it falls back to the most recent block.
	var blockID string
	var relatedBlocks []string
	if !pass {
		argsStr := fmt.Sprintf("%v", args)
		for _, r := range result.Related {
			if c := e.guard.Find(r.ID); c != nil && argsContainBlockData(argsStr, c.Text) {
				relatedBlocks = append(relatedBlocks, c.ID)
			}
		}
		switch {
		case len(relatedBlocks) > 0:
			blockID = relatedBlocks[0]
		case len(result.Related) > 0:
			blockID = result.Related[len(result.Related)-1].ID
		}
	}

	var flags []string
	var skipReason string
	if pass {
		skipReason = det.Rationale
	} else {
		flags = dedupe(strings.Split(det.Rationale, ", "))
	}
	e.logSecurityStatic(toolName, blockID, relatedBlocks, pass, flags, skipReason, e.taintLineage(relatedBlocks))

	checkPath := "static"
	for _, f := range result.Findings[1:] {
		switch f.Source {
		case stageScreener:
			checkPath += "→triage"
			suspicious := f.Verdict != contentguard.Allow
			skip := ""
			if !suspicious {
				skip = "triage_benign"
			}
			in, out := usage.get(stageScreener)
			e.logSecurityTriage(toolName, blockID, suspicious, "triage", f.Latency.Milliseconds(), in, out, skip)
		case stageReviewer:
			checkPath += "→supervisor"
			in, out := usage.get(stageReviewer)
			e.logSecuritySupervisor(toolName, blockID, strings.ToUpper(string(f.Verdict)), f.Rationale, "supervisor", f.Latency.Milliseconds(), in, out)
		}
	}

	if result.Verdict != contentguard.Allow {
		e.logSecurityDecision(toolName, "deny", result.Rationale, "", checkPath)
		if e.metricsCollector != nil {
			e.metricsCollector.RecordSupervision(false)
		}
		return nil, fmt.Errorf("security: %s", result.Rationale)
	}

	e.logSecurityDecision(toolName, "allow", "verified", "", checkPath)
	if e.metricsCollector != nil {
		e.metricsCollector.RecordSupervision(true)
	}
	return relatedBlocks, nil
}

// dedupe removes repeated entries, keeping first-seen order.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// argsContainBlockData checks if tool arguments contain data from a block.
// This is a simple substring match - full taint tracking would be more sophisticated.
// Re-implemented from the old agentkit security verifier (A-G6).
func argsContainBlockData(argsStr, content string) bool {
	// Extract meaningful substrings from block content to check
	// For URLs, check if any URL from the block appears in args
	for _, url := range extractURLs(content) {
		if len(url) > 20 && containsIgnoreCase(argsStr, url) {
			return true
		}
	}

	// For other content, check if significant portions appear
	// (Skip very short content to avoid false positives)
	if len(content) > 100 {
		// Check if a meaningful chunk of block content appears in args
		// Use a sliding window of 50 chars
		for i := 0; i+50 <= len(content) && i < 500; i += 25 {
			chunk := content[i : i+50]
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
	for _, word := range strings.Fields(content) {
		word = strings.Trim(word, `"',[]{}()`)
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			for _, term := range []string{`"`, `'`, `>`, ` `, `\n`} {
				if idx := strings.Index(word, term); idx > 0 {
					word = word[:idx]
				}
			}
			urls = append(urls, word)
		}
	}
	return urls
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// taintLineage builds the taint dependency trees for the given blocks.
func (e *Executor) taintLineage(blockIDs []string) []session.TaintNode {
	var nodes []session.TaintNode
	for _, id := range blockIDs {
		if n, ok := lineageTree(e.guard.Find(id), 0, make(map[string]bool)); ok {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// lineageTree recursively builds the taint tree for a block.
// visited prevents infinite loops from circular taint references.
func lineageTree(c *contentguard.Content, depth int, visited map[string]bool) (session.TaintNode, bool) {
	if c == nil || visited[c.ID] {
		return session.TaintNode{}, false
	}
	visited[c.ID] = true

	node := session.TaintNode{
		BlockID: c.ID,
		Trust:   string(c.Trust),
		Source:  c.Source,
		Depth:   depth,
	}
	for _, parent := range c.Origins {
		if p, ok := lineageTree(parent, depth+1, visited); ok {
			node.TaintedBy = append(node.TaintedBy, p)
		}
	}
	return node, true
}

// AddUntrustedContent registers untrusted content with the content guard.
func (e *Executor) AddUntrustedContent(ctx context.Context, content, source string) {
	e.AddUntrustedContentWithTaint(ctx, content, source, nil)
}

// AddUntrustedContentWithTaint registers untrusted content with explicit taint lineage.
func (e *Executor) AddUntrustedContentWithTaint(ctx context.Context, content, source string, taintedBy []string) {
	if e.guard == nil {
		return
	}
	// Agent role is recorded on the block's XML for forensics only; the guard
	// itself no longer filters content per agent (A-G2).
	agentContext := getAgentIdentity(ctx).Role

	block := e.guard.IngestWithLineage(
		contentguard.Untrusted,
		contentguard.Data,
		true,
		content,
		source,
		taintedBy, // Parent blocks that influenced this content
	)

	// Log to session with XML representation including taint info
	taintAttr := ""
	if len(taintedBy) > 0 {
		taintAttr = fmt.Sprintf(` tainted-by="%s"`, strings.Join(taintedBy, ","))
	}
	xmlBlock := fmt.Sprintf(`<block id="%s" trust="untrusted" type="data" source="%s" mutable="true" agent="%s"%s>%s</block>`,
		block.ID, source, agentContext, taintAttr, truncateForLog(content, 200))
	entropy := contentguard.ShannonEntropy(content)
	e.logSecurityBlockWithTaint(block.ID, "untrusted", "data", source, xmlBlock, entropy, taintedBy)
}
