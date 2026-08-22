package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/tools"
)

func writeSkill(t *testing.T, name string, withScript bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: test skill\n---\nDo the thing carefully.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if withScript {
		os.MkdirAll(filepath.Join(dir, "scripts"), 0o755)
		os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	}
	return dir
}

func TestSkillActivation_LoadsAndInjectsContext(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:  "x",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Work"}},
	}
	dir := writeSkill(t, "code-review", true)
	var seen []string
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		last := req.Messages[len(req.Messages)-1].Content
		seen = append(seen, last)
		if strings.Contains(last, "[Skill loaded: code-review]") {
			return &llm.ChatResponse{Content: "done with skill"}, nil
		}
		return &llm.ChatResponse{Content: "I will [use-skill:code-review] now"}, nil
	})
	exec := New(Config{Workflow: wf, Model: model, SkillRefs: []skills.SkillRef{{Name: "code-review", Description: "Review code", Path: dir}}})
	var loaded string
	exec.Hooks().On(hooks.SkillLoaded, func(_ context.Context, evt hooks.Event) { loaded = evt.Data["name"].(string) })

	res, err := exec.Run(context.Background(), nil)
	if err != nil || res.Outputs["g"] != "done with skill" {
		t.Fatalf("got %+v %v", res, err)
	}
	if loaded != "code-review" {
		t.Errorf("SkillLoaded hook = %q", loaded)
	}
	ctx := seen[len(seen)-1]
	if !strings.Contains(ctx, "Do the thing carefully.") || !strings.Contains(ctx, "## Available Scripts") || !strings.Contains(ctx, "run.sh") {
		t.Errorf("skill context not injected: %q", ctx)
	}

	// Second activation is served from the cache; unknown/broken skills are ignored.
	if exec.checkSkillActivation("[use-skill:code-review]") == nil {
		t.Error("expected cached skill")
	}
	if exec.checkSkillActivation("[use-skill:unknown]") != nil {
		t.Error("unknown skill must not activate")
	}
	exec.skillRefs = append(exec.skillRefs, skills.SkillRef{Name: "broken", Path: t.TempDir()})
	if exec.checkSkillActivation("[use-skill:broken]") != nil {
		t.Error("unloadable skill must not activate")
	}
}

func TestInterpolate_UnresolvedWarnsToSession(t *testing.T) {
	sess := &session.Session{}
	exec := New(Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), Session: sess})
	exec.inputs = map[string]string{"a": "1"}
	exec.outputs = map[string]string{"b": "2"}
	if got := exec.interpolate("$a+$b=$c"); got != "1+2=$c" {
		t.Errorf("got %q", got)
	}
	warns := eventsOfType(sess, session.EventWarning)
	if len(warns) != 1 || !strings.Contains(warns[0].Content, "Unresolved variables: [c]") {
		t.Errorf("expected unresolved warning, got %+v", warns)
	}
}

func TestStructuredOutputHelpers(t *testing.T) {
	if buildStructuredOutputInstruction(nil) != "" {
		t.Error("no outputs => no instruction")
	}
	got, err := parseStructuredOutput("plain text answer", []string{"a", "b"})
	if err != nil || got["a"] != "plain text answer" || got["b"] != "plain text answer" {
		t.Errorf("plain-text fallback: %v %v", got, err)
	}
	got, _ = parseStructuredOutput(`{"a": 1}`, []string{"a", "missing"})
	if got["a"] != "1" {
		t.Errorf("non-string field: %v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Error("missing fields must be absent")
	}
}

func TestSecurityResearchPrefix(t *testing.T) {
	exec := NewExecutor(&agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	if exec.securityResearchPrefix() != "" {
		t.Error("no scope => no prefix")
	}
	exec.securityResearchScope = "lab"
	if p := exec.securityResearchPrefix(); !strings.Contains(p, "Authorized Scope: lab") {
		t.Errorf("got %q", p)
	}
	// The prefix reaches goal, dynamic sub-agent and static agent prompts.
	var systems []string
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		systems = append(systems, req.Messages[0].Content)
		return &llm.ChatResponse{Content: "ok"}, nil
	})
	exec = New(Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model, Security: &SecurityConfig{Mode: SecurityResearch, Scope: "lab"}})
	exec.spawnDynamicAgent(context.Background(), "r", "t", nil)
	exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "t", nil, "", []GoalOutput{{ID: "prev", Output: "o"}}, false)
	for _, s := range systems {
		if !strings.Contains(s, "Authorized Scope: lab") {
			t.Errorf("prefix missing from system prompt: %q", s)
		}
	}
}

func TestGuidancePrefixes_ForSubAgents(t *testing.T) {
	recall := fakeTool{name: "recall", run: func(context.Context, tools.Args) (string, error) { return "", nil }}
	pad := fakeTool{name: "scratchpad_write", run: func(context.Context, tools.Args) (string, error) { return "", nil }}
	reg, _ := newTestRegistry(t, t.TempDir(), recall, pad)
	var system string
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		system = req.Messages[0].Content
		return &llm.ChatResponse{Content: "ok"}, nil
	})
	exec := New(Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model, Registry: reg, WorkspaceContext: "WS"})
	if _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "t", []string{"f"}, "", nil, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PERSISTENT KNOWLEDGE BASE", "SCRATCHPAD"} {
		if !strings.Contains(system, want) {
			t.Errorf("expected %q guidance in sub-agent prompt", want)
		}
	}
	if _, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "WS") {
		t.Error("expected workspace context in dynamic sub-agent prompt")
	}
}

func TestRecordLLMMetrics(t *testing.T) {
	exec := NewExecutor(&agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	exec.recordLLMMetrics(&llm.ChatResponse{}, time.Millisecond) // no collector
	m := &recordingMetrics{}
	exec.SetMetricsCollector(m)
	exec.recordLLMMetrics(nil, time.Millisecond) // nil response
	exec.recordLLMMetrics(&llm.ChatResponse{InputTokens: 1}, time.Millisecond)
}

func TestExecuteSimpleParallel_AgentErrorAndSynthesisError(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:   "x",
		Agents: []agentfile.Agent{{Name: "a1", Requires: "fast"}, {Name: "a2"}},
		Steps:  []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals:  []agentfile.Goal{{Name: "g", Outcome: "Work", UsingAgent: []string{"a1", "a2"}}},
	}
	// Profile resolution failure surfaces as the goal error.
	exec := New(Config{Workflow: wf, Model: llmmock.New(), Resolver: fakeResolver{err: errors.New("no such profile")}})
	if _, err := exec.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "no such profile") {
		t.Fatalf("expected profile error, got %v", err)
	}

	// Synthesis LLM failure.
	fast := llmmock.New()
	fast.SetResponse("fast")
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		if strings.Contains(req.Messages[len(req.Messages)-1].Content, "Synthesize") {
			return nil, errors.New("synthesis down")
		}
		return &llm.ChatResponse{Content: "agent"}, nil
	})
	exec = New(Config{Workflow: wf, Model: model, Resolver: fakeResolver{models: map[string]llm.Model{"fast": fast, "": model}}})
	if _, err := exec.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "synthesis down") {
		t.Fatalf("expected synthesis error, got %v", err)
	}

	// Single agent returns its output directly, no synthesis.
	single := &agentfile.Workflow{
		Name:   "x",
		Agents: []agentfile.Agent{{Name: "only"}},
		Steps:  wf.Steps,
		Goals:  []agentfile.Goal{{Name: "g", Outcome: "Work", UsingAgent: []string{"only"}}},
	}
	exec = New(Config{Workflow: single, Model: model})
	res, err := exec.Run(context.Background(), nil)
	if err != nil || res.Outputs["g"] != "agent" {
		t.Fatalf("got %+v %v", res, err)
	}
}
