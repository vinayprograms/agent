package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// modelFunc adapts a function to llm.Model. Unlike llmmock it keeps no
// state, so it is safe for tests that call it from parallel sub-agents.
type modelFunc func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)

func (f modelFunc) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return f(ctx, req)
}

// mustNew constructs an executor or fails the test.
func mustNew(t *testing.T, cfg Config) *Executor {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

// mustNewExecutor is the old four-argument constructor shape for tests.
func mustNewExecutor(t *testing.T, wf *agentfile.Workflow, model llm.Model, reg *tools.Registry, pol *policy.Policy) *Executor {
	t.Helper()
	return mustNew(t, Config{Workflow: wf, Model: model, Registry: reg, Policy: pol})
}

// denyingReviewer is the default reviewer for security tests: escalations
// are denied unless a test supplies its own reviewer.
func denyingReviewer() *llmmock.Model {
	m := llmmock.New()
	m.SetResponse("DENY: test default reviewer")
	return m
}

// permissivePolicy returns a policy with every tool enabled (the old
// policy.New() default).
func permissivePolicy() *policy.Policy {
	pol := policy.New()
	pol.DefaultDeny = false
	return pol
}

// newTestRegistry builds a small real registry rooted at ws: read, ls, pwd,
// the spawn_agents binder, plus any extra tools.
func newTestRegistry(t *testing.T, ws string, extra ...tools.Tool) (*tools.Registry, *tools.SpawnBinder) {
	t.Helper()
	reg := tools.NewRegistry()
	binder := tools.NewSpawnBinder()
	all := append([]tools.Tool{tools.Read(ws), tools.Ls(ws), tools.Pwd(), binder.Tool()}, extra...)
	for _, tl := range all {
		if err := reg.Register(tools.New(tl)); err != nil {
			t.Fatalf("register %s: %v", tl.Name(), err)
		}
	}
	return reg, binder
}

// fakeTool is a minimal tools.Tool for tests.
type fakeTool struct {
	name   string
	params map[string]tools.Param
	run    func(ctx context.Context, args tools.Args) (string, error)
}

func (f fakeTool) Name() string                       { return f.name }
func (f fakeTool) Description() string                { return "fake " + f.name }
func (f fakeTool) Parameters() map[string]tools.Param { return f.params }
func (f fakeTool) Execute(ctx context.Context, args tools.Args) (string, error) {
	return f.run(ctx, args)
}

// TestMergeAgentOutputs_NestsUnderGoalOutput is the 39-mixed-supervision
// case: `GOAL scan -> scan_results USING scanner` where
// `AGENT scanner -> vulnerabilities, severity`. The scanner emits its own
// field names, so the goal's declared output has to be built from them.
// Before this, scan_results stayed empty and the goal was reported
// empty_output with a non-zero exit while the work had actually been done.
func TestMergeAgentOutputs_NestsUnderGoalOutput(t *testing.T) {
	agentVars := map[string]map[string]string{
		"scanner": {"vulnerabilities": "SQL injection in login", "severity": "high"},
	}
	got := mergeAgentOutputs(nil, []string{"scan_results"}, agentVars)

	raw, ok := got["scan_results"]
	if !ok || strings.TrimSpace(raw) == "" {
		t.Fatalf("scan_results empty; got %#v", got)
	}
	var nested map[string]string
	if err := json.Unmarshal([]byte(raw), &nested); err != nil {
		t.Fatalf("scan_results is not a JSON object: %v (%q)", err, raw)
	}
	if nested["vulnerabilities"] != "SQL injection in login" || nested["severity"] != "high" {
		t.Errorf("nested payload lost fields: %#v", nested)
	}
}

// TestMergeAgentOutputs_ByName covers the direct case: a goal output whose
// name matches an agent field takes that value as-is, not wrapped.
func TestMergeAgentOutputs_ByName(t *testing.T) {
	agentVars := map[string]map[string]string{
		"scanner": {"vulnerabilities": "none found", "severity": "low"},
	}
	got := mergeAgentOutputs(nil, []string{"severity"}, agentVars)
	if got["severity"] != "low" {
		t.Errorf("severity = %q, want %q (matched by name, not nested)", got["severity"], "low")
	}
}

// TestMergeAgentOutputs_GoalWins asserts the goal's own outputs are not
// overwritten. A goal that emitted its outputs has said what it meant;
// agent outputs are a fallback for what it left empty.
func TestMergeAgentOutputs_GoalWins(t *testing.T) {
	vars := map[string]string{"summary": "the goal's own synthesis", "confidence": ""}
	agentVars := map[string]map[string]string{
		"researcher": {"summary": "an agent's take", "confidence": "0.8"},
	}
	got := mergeAgentOutputs(vars, []string{"summary", "confidence"}, agentVars)
	if got["summary"] != "the goal's own synthesis" {
		t.Errorf("goal output overwritten: %q", got["summary"])
	}
	if got["confidence"] != "0.8" {
		t.Errorf("empty goal output not filled from agent: %q", got["confidence"])
	}
}

// TestMergeAgentOutputs_MultipleAgentsKeyedByName: with several agents the
// nested payload is keyed by agent name, so siblings sharing a field name
// don't silently clobber each other.
func TestMergeAgentOutputs_MultipleAgentsKeyedByName(t *testing.T) {
	agentVars := map[string]map[string]string{
		"researcher": {"findings": "A"},
		"critic":     {"findings": "B"},
	}
	got := mergeAgentOutputs(nil, []string{"analysis"}, agentVars)
	var nested map[string]map[string]string
	if err := json.Unmarshal([]byte(got["analysis"]), &nested); err != nil {
		t.Fatalf("not keyed by agent: %v (%q)", err, got["analysis"])
	}
	if nested["researcher"]["findings"] != "A" || nested["critic"]["findings"] != "B" {
		t.Errorf("lost a sibling's output: %#v", nested)
	}
}

// TestMergeAgentOutputs_NoAgents is the no-op guard: without agent outputs
// the goal's own vars pass through untouched.
func TestMergeAgentOutputs_NoAgents(t *testing.T) {
	vars := map[string]string{"x": "y"}
	got := mergeAgentOutputs(vars, []string{"x"}, nil)
	if got["x"] != "y" || len(got) != 1 {
		t.Errorf("no-op case altered vars: %#v", got)
	}
}
