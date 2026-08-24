package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/step"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/tools"
)

func TestToolConcurrency_Clamps(t *testing.T) {
	for _, tt := range []struct{ cpus, want int }{{0, 4}, {1, 4}, {2, 8}, {8, 32}, {64, 32}} {
		if got := toolConcurrency(tt.cpus); got != tt.want {
			t.Errorf("toolConcurrency(%d) = %d, want %d", tt.cpus, got, tt.want)
		}
	}
}

func TestRun_UnnamedWorkflow(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{}, llmmock.New(), nil, nil)
	res, err := exec.Run(t.Context(), RunOptions{})
	if err != nil || res.Status != StatusComplete {
		t.Fatalf("got %+v %v", res, err)
	}
}

// ExecuteGoal adopts step inputs it has not seen and overwrites its outputs.
func TestExecuteGoal_SyncsStepState(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:  "x",
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Use $fresh"}},
	}
	provider := llmmock.New()
	provider.SetResponse("done")
	exec := mustNewExecutor(t, wf, provider, nil, nil)
	exec.inputs = map[string]string{"kept": "1"}

	state := step.NewState(map[string]string{"fresh": "value", "kept": "2"})
	state.Outputs["earlier"] = "prior output"
	if err := exec.ExecuteGoal(t.Context(), "g", state); err != nil {
		t.Fatalf("ExecuteGoal: %v", err)
	}
	if exec.inputs["fresh"] != "value" || exec.inputs["kept"] != "1" {
		t.Errorf("inputs = %v, want fresh adopted and kept untouched", exec.inputs)
	}
	if exec.outputs["earlier"] != "prior output" || state.Outputs["g"] != "done" {
		t.Errorf("outputs = %v, state = %v", exec.outputs, state.Outputs)
	}
}

// Structured outputs are parsed on every goal shape; a malformed answer is a
// warning, not a failure.
func TestGoalOutputs_ParsedOrWarned(t *testing.T) {
	outputs := []string{"answer"}
	structured := `{"answer": "42"}`
	limit := 1

	tests := []struct {
		name    string
		goal    agentfile.Goal
		content string
		want    string
	}{
		{"converge parsed", agentfile.Goal{Name: "g", Outcome: "o", IsConverge: true, WithinLimit: &limit, Outputs: outputs}, structured, "42"},
		{"converge unparsable", agentfile.Goal{Name: "g", Outcome: "o", IsConverge: true, WithinLimit: &limit, Outputs: outputs}, "no json here", "no json here"},
		{"multi-agent parsed", agentfile.Goal{Name: "g", Outcome: "o", UsingAgent: []string{"a"}, Outputs: outputs}, structured, "42"},
		{"multi-agent unparsable", agentfile.Goal{Name: "g", Outcome: "o", UsingAgent: []string{"a"}, Outputs: outputs}, "no json here", "no json here"},
		{"plain goal parsed", agentfile.Goal{Name: "g", Outcome: "o", Outputs: outputs}, structured, "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &agentfile.Workflow{Name: "x", Agents: []agentfile.Agent{{Name: "a"}}, Goals: []agentfile.Goal{tt.goal}}
			provider := llmmock.New()
			provider.SetResponse(tt.content)
			sess := &session.Session{}
			exec := mustNew(t, Config{Workflow: wf, Model: provider, Session: sess})

			if _, err := exec.executeGoalWithTracking(t.Context(), &wf.Goals[0]); err != nil {
				t.Fatalf("executeGoalWithTracking: %v", err)
			}
			if got := exec.outputs["answer"]; got != tt.want {
				t.Errorf("outputs[answer] = %q, want %q", got, tt.want)
			}
		})
	}
}

// The supervisor's correction is re-executed, and its structured output is
// what the goal reports.
func TestGoalReorient_ParsesCorrectedOutput(t *testing.T) {
	wf := supervisedWorkflow()
	wf.Goals[0].Outputs = []string{"answer"}
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a","confidence":"high"}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: `{"met_commitment":true}`}, nil
		case strings.Contains(user, "do better"):
			return &llm.ChatResponse{Content: `{"answer": "corrected"}`}, nil
		}
		return &llm.ChatResponse{Content: `{"answer": "draft"}`}, nil
	})
	exec, _ := newSupervisedExecutor(t, wf, model, fakeSupervisor{supervise: true, verdict: "REORIENT"})
	if _, err := exec.Run(t.Context(), RunOptions{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exec.outputs["answer"] != "corrected" {
		t.Errorf("outputs = %v, want the corrected answer", exec.outputs)
	}
}

// Prompt prefixes are additive: each capability the registry advertises adds
// its guidance, on top of the research-scope framing.
func TestSystemPrompt_CapabilityPrefixes(t *testing.T) {
	ws := t.TempDir()
	reg, _ := newTestRegistry(t, ws,
		fakeTool{name: "recall", run: func(context.Context, tools.Args) (string, error) { return "", nil }},
		fakeTool{name: "scratchpad_write", run: func(context.Context, tools.Args) (string, error) { return "", nil }},
	)

	var system string
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		system = req.Messages[0].Content
		return &llm.ChatResponse{Content: "done"}, nil
	})
	wf := &agentfile.Workflow{
		Name:  "x",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Work"}},
	}
	exec := mustNew(t, Config{
		Workflow: wf, Model: model, Registry: reg, Policy: permissivePolicy(),
		WorkspaceContext: "<workspace/>",
		Security:         &SecurityConfig{Mode: SecurityResearch, Scope: "OWASP", Reviewer: denyingReviewer()},
	})
	if _, err := exec.Run(t.Context(), RunOptions{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"OWASP", OrchestratorSystemPromptPrefix, SemanticMemoryGuidancePrefix, ScratchpadGuidancePrefix, "<workspace/>"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt missing %q:\n%s", want, system)
		}
	}
}

// Interrupts pushed while tools are running are injected after the tool
// results, without ending the turn.
func TestExecutePhase_InterruptsAfterToolCalls(t *testing.T) {
	buf := NewInterruptBuffer()
	reg, _ := newTestRegistry(t, t.TempDir())
	var turns int
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		turns++
		switch turns {
		case 1:
			buf.Push(InterruptMessage{From: "peer", Content: "urgent rethink"})
			return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "pwd", Args: map[string]any{}}}}, nil
		default:
			last := req.Messages[len(req.Messages)-1].Content
			if !strings.Contains(last, "urgent rethink") {
				t.Errorf("turn %d did not carry the interrupt: %q", turns, last)
			}
			return &llm.ChatResponse{Content: "acknowledged"}, nil
		}
	})
	wf := &agentfile.Workflow{
		Name:  "x",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Work"}},
	}
	exec := mustNew(t, Config{Workflow: wf, Model: model, Registry: reg, Policy: permissivePolicy()})
	res, err := exec.Run(t.Context(), RunOptions{Interrupts: buf})
	if err != nil || res.Outputs["g"] != "acknowledged" {
		t.Fatalf("got %+v %v", res, err)
	}
}

// failingStore accepts nothing: every persist fails, and the trail is empty.
type failingStore struct{}

func (failingStore) SavePre(*checkpoint.PreCheckpoint) error   { return errors.New("no pre") }
func (failingStore) SavePost(*checkpoint.PostCheckpoint) error { return errors.New("no post") }
func (failingStore) SaveReconcile(*checkpoint.ReconcileResult) error {
	return errors.New("no reconcile")
}
func (failingStore) SaveSupervise(*checkpoint.SuperviseResult) error {
	return errors.New("no supervise")
}
func (failingStore) Trail() []checkpoint.Checkpoint { return nil }

// goalPausingSupervisor pauses the goal but lets its sub-agents through.
type goalPausingSupervisor struct{}

func (goalPausingSupervisor) Reconcile(pre *checkpoint.PreCheckpoint, _ *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult {
	return &checkpoint.ReconcileResult{StepID: pre.StepID, Supervise: true}
}

func (goalPausingSupervisor) Supervise(_ context.Context, req supervision.SuperviseRequest) (*checkpoint.SuperviseResult, error) {
	verdict := "CONTINUE"
	if !strings.HasPrefix(req.Pre.StepID, "subagent:") {
		verdict = "PAUSE"
	}
	return &checkpoint.SuperviseResult{StepID: req.Pre.StepID, Verdict: verdict, Question: "why?"}, nil
}

// A multi-agent goal supervises around the parallel agents. Checkpoint
// persistence is best-effort: failures are logged, execution continues.
func TestMultiAgentGoal_SupervisionAndCheckpointFailures(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "x", Supervised: true,
		Agents: []agentfile.Agent{{Name: "a1"}},
		Steps:  []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals:  []agentfile.Goal{{Name: "g", Outcome: "Work", UsingAgent: []string{"a1"}}},
	}
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a"}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: "not json"}, nil
		}
		return &llm.ChatResponse{Content: "agent output"}, nil
	})

	newExec := func(verdict string) *Executor {
		return mustNew(t, Config{
			Workflow: wf, Model: model, Session: &session.Session{},
			CheckpointStore: failingStore{},
			Supervisor:      fakeSupervisor{supervise: true, verdict: verdict},
		})
	}

	out, err := newExec("CONTINUE").executeMultiAgentGoal(t.Context(), &wf.Goals[0])
	if err != nil || out != "agent output" {
		t.Fatalf("got %q %v", out, err)
	}

	// PAUSE at the goal level only: the sub-agents themselves run to
	// completion, and the goal stops on the supervisor's verdict.
	pauseGoalOnly := mustNew(t, Config{
		Workflow: wf, Model: model, Session: &session.Session{},
		CheckpointStore: failingStore{},
		Supervisor:      goalPausingSupervisor{},
	})
	if _, err := pauseGoalOnly.executeMultiAgentGoal(t.Context(), &wf.Goals[0]); err == nil || !strings.Contains(err.Error(), "supervision paused") {
		t.Fatalf("expected pause, got %v", err)
	}

	// A supervisor that errors is logged and the output stands.
	exec := mustNew(t, Config{
		Workflow: wf, Model: model, Session: &session.Session{},
		CheckpointStore: failingStore{},
		Supervisor:      fakeSupervisor{supervise: true, verdict: "CONTINUE", err: errors.New("supervisor down")},
	})
	exec.currentGoal = "g"
	if _, err := exec.executeMultiAgentGoal(t.Context(), &wf.Goals[0]); err != nil {
		t.Fatalf("supervisor failure must not fail the goal: %v", err)
	}
}

func TestInterpolate_ResolvesNestedReferences(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	exec.inputs = map[string]string{"topic": "go"}
	exec.outputs = map[string]string{"draft": "about $topic"}

	if got := exec.interpolate("$topic and $draft"); got != "go and about $topic" {
		t.Errorf("interpolate = %q, want %q", got, "go and about $topic")
	}
	if got := exec.interpolate("$unknown"); got != "$unknown" {
		t.Errorf("unresolved variable = %q, want it left as-is", got)
	}
}

func TestBuildPriorGoalsContext(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	if got := exec.buildPriorGoalsContext(); got != nil {
		t.Errorf("no outputs = %v, want nil", got)
	}
	exec.outputs = map[string]string{"g": "out"}
	got := exec.buildPriorGoalsContext()
	if len(got) != 1 || got[0].ID != "g" || got[0].Output != "out" {
		t.Errorf("buildPriorGoalsContext() = %+v", got)
	}
}

// Sub-agents report their count to the metrics collector as they start and
// finish, and carry the parent's inputs and outputs into their brief.
func TestSpawnAgent_MetricsAndContext(t *testing.T) {
	metrics := &recordingMetrics{}
	var task string
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		task = req.Messages[len(req.Messages)-1].Content
		return &llm.ChatResponse{Content: "sub output"}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model, MetricsCollector: metrics})
	exec.inputs = map[string]string{"in": "1"}
	exec.outputs = map[string]string{"prior": "earlier output"}

	out, _, err := exec.spawnAgentWithPrompt(t.Context(), "researcher", "sys", "find things", nil, "", exec.buildPriorGoalsContext(), false, 0)
	if err != nil || out != "sub output" {
		t.Fatalf("got %q %v", out, err)
	}
	if !strings.Contains(task, "earlier output") {
		t.Errorf("sub-agent brief lacks prior goal output: %q", task)
	}
	if metrics.maxSubagents == 0 {
		t.Error("sub-agent count never reported")
	}
}

// A sub-agent reorientation re-runs the task with the correction; failures in
// that re-run surface to the caller.
func TestSpawnAgent_ReorientAndPipelineErrors(t *testing.T) {
	newModel := func(correctedErr error) llm.Model {
		return modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			user := req.Messages[len(req.Messages)-1].Content
			switch {
			case strings.Contains(user, "declare your intent"):
				return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a"}`}, nil
			case strings.Contains(user, "Assess your work"):
				return &llm.ChatResponse{Content: `{"met_commitment":true}`}, nil
			case strings.Contains(user, "do better"):
				if correctedErr != nil {
					return nil, correctedErr
				}
				return &llm.ChatResponse{Content: "corrected"}, nil
			}
			return &llm.ChatResponse{Content: "draft"}, nil
		})
	}
	wf := supervisedWorkflow()

	exec, _ := newSupervisedExecutor(t, wf, newModel(nil), fakeSupervisor{supervise: true, verdict: "REORIENT"})
	exec.currentGoalSupervised = true
	out, err := exec.spawnDynamicAgent(t.Context(), "r", "task", nil)
	if err != nil || out != "corrected" {
		t.Fatalf("dynamic reorient: %q %v", out, err)
	}

	exec, _ = newSupervisedExecutor(t, wf, newModel(errors.New("model down")), fakeSupervisor{supervise: true, verdict: "REORIENT"})
	exec.currentGoalSupervised = true
	if _, err := exec.spawnDynamicAgent(t.Context(), "r", "task", nil); err == nil {
		t.Error("expected the corrected run's failure to surface")
	}

	exec, _ = newSupervisedExecutor(t, wf, newModel(errors.New("model down")), fakeSupervisor{supervise: true, verdict: "REORIENT"})
	if _, _, err := exec.spawnAgentWithPrompt(t.Context(), "r", "sys", "task", nil, "", nil, true, 0); err == nil {
		t.Error("expected the corrected run's failure to surface (static agent)")
	}

	// A failure inside the pipeline's EXECUTE phase surfaces to the caller.
	failing := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, errors.New("model down")
	})
	exec, _ = newSupervisedExecutor(t, wf, failing, fakeSupervisor{supervise: true, verdict: "CONTINUE"})
	if _, _, err := exec.spawnAgentWithPrompt(t.Context(), "r", "sys", "task", nil, "", nil, true, 0); err == nil {
		t.Error("expected the pipeline error to surface")
	}
}

// The sub-agent loop keeps going while the model asks for tools and reports
// every tool it used when it finally answers.
func TestSubAgentExecutePhase_ToolLoop(t *testing.T) {
	reg, _ := newTestRegistry(t, t.TempDir())
	var turns int
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		turns++
		if turns == 1 {
			return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "pwd", Args: map[string]any{}}}}, nil
		}
		return &llm.ChatResponse{Content: "sub done"}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model, Registry: reg, Policy: permissivePolicy()})
	out, toolsUsed, _, _, err := exec.subAgentExecutePhaseWithModel(t.Context(), model, "r", "sys", "task", 0)
	if err != nil || out != "sub done" {
		t.Fatalf("got %q %v", out, err)
	}
	if len(toolsUsed) != 1 || toolsUsed[0] != "pwd" {
		t.Errorf("toolsUsed = %v, want [pwd]", toolsUsed)
	}
}

// A commit response that names no confidence defaults to medium.
func TestSubAgentCommitPhase_DefaultConfidence(t *testing.T) {
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a"}`}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model})
	pre := exec.subAgentCommitPhase(t.Context(), "r", "task")
	if pre.Confidence != "medium" {
		t.Errorf("Confidence = %q, want medium", pre.Confidence)
	}
}

// An unparsable self-assessment is treated as "commitment met" rather than
// blocking the sub-agent.
func TestSubAgentPostCheckpoint_UnparsableAssessment(t *testing.T) {
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "not json"}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model})
	pre := &checkpoint.PreCheckpoint{StepID: "subagent:r"}
	post := exec.subAgentPostCheckpoint(t.Context(), "r", pre, "output", nil)
	if !post.MetCommitment {
		t.Error("unparsable assessment should default to met")
	}
}

func TestCreatePostCheckpoint_UnparsableAssessment(t *testing.T) {
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "not json"}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model})
	goal := &agentfile.Goal{Name: "g", Outcome: "o"}
	post := exec.createPostCheckpoint(t.Context(), goal, &checkpoint.PreCheckpoint{StepID: "g"}, "output", nil)
	if !post.MetCommitment {
		t.Error("unparsable assessment should default to met")
	}
}

var _ supervision.Store = failingStore{}

func TestConvergePrompt_CarriesPriorGoals(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	exec.outputs = map[string]string{"earlier": "earlier output"}
	got := exec.buildConvergePrompt(&agentfile.Goal{Name: "g", Outcome: "o", Outputs: []string{"answer"}}, nil, "")
	if !strings.Contains(got, "earlier output") || !strings.Contains(got, "answer") {
		t.Errorf("converge prompt missing prior goal or output instruction:\n%s", got)
	}
}

func TestGoalTracking_ErrorPaths(t *testing.T) {
	zero := 0
	// A CONVERGE goal that cannot run fails the goal.
	wf := &agentfile.Workflow{Name: "x", Goals: []agentfile.Goal{{Name: "g", Outcome: "o", IsConverge: true, WithinLimit: &zero}}}
	exec := mustNewExecutor(t, wf, llmmock.New(), nil, nil)
	if _, err := exec.executeGoalWithTracking(t.Context(), &wf.Goals[0]); err == nil {
		t.Error("expected the converge failure to surface")
	}

	// A multi-agent goal naming an unknown agent fails the goal.
	wf = &agentfile.Workflow{Name: "x", Goals: []agentfile.Goal{{Name: "g", Outcome: "o", UsingAgent: []string{"ghost"}}}}
	exec = mustNewExecutor(t, wf, llmmock.New(), nil, nil)
	if _, err := exec.executeGoalWithTracking(t.Context(), &wf.Goals[0]); err == nil {
		t.Error("expected the missing-agent failure to surface")
	}

	// A correction that the model cannot answer fails the goal.
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"interpretation":"i","approach":"a"}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: `{"met_commitment":true}`}, nil
		case strings.Contains(user, "do better"):
			return nil, errors.New("model down")
		}
		return &llm.ChatResponse{Content: "draft"}, nil
	})
	sup, _ := newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "REORIENT"})
	if _, err := sup.Run(t.Context(), RunOptions{}); err == nil {
		t.Error("expected the correction failure to surface")
	}
}

// Self-assessments that look like JSON but are not parse as "commitment met".
func TestPostCheckpoints_MalformedJSON(t *testing.T) {
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "{not really json}"}, nil
	})
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: model})

	post := exec.createPostCheckpoint(t.Context(), &agentfile.Goal{Name: "g", Outcome: "o"}, &checkpoint.PreCheckpoint{StepID: "g"}, "out", nil)
	if !post.MetCommitment {
		t.Error("goal: malformed assessment should default to met")
	}
	subPost := exec.subAgentPostCheckpoint(t.Context(), "r", &checkpoint.PreCheckpoint{StepID: "subagent:r"}, "out", nil)
	if !subPost.MetCommitment {
		t.Error("sub-agent: malformed assessment should default to met")
	}
}

func TestExecuteTool_TimeoutAndUnguardedExternalResult(t *testing.T) {
	fetch := fakeTool{name: "web_fetch", run: func(context.Context, tools.Args) (string, error) { return "external content", nil }}
	reg, _ := newTestRegistry(t, t.TempDir(), fetch)
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(),
		Registry: reg, Policy: permissivePolicy(), TimeoutWebFetch: 5,
	})
	// No guard configured: the external result is simply not registered.
	out, err := exec.executeTool(t.Context(), llm.ToolCallResponse{Name: "web_fetch"})
	if err != nil || out != "external content" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestExecuteToolsParallel_ErrorBecomesContent(t *testing.T) {
	ok := fakeTool{name: "ok", run: func(context.Context, tools.Args) (string, error) { return "fine", nil }}
	exec, _ := newToolExecutor(t, ok)
	msgs := exec.executeToolsParallel(t.Context(), []llm.ToolCallResponse{
		{ID: "1", Name: "ok"},
		{ID: "2", Name: "missing"},
	})
	if msgs[0].Content != "fine" || !strings.HasPrefix(msgs[1].Content, "Error:") {
		t.Errorf("got %+v", msgs)
	}
}

// A failing MCP server surfaces its error to the caller.
func TestExecuteMCPTool_ServerError(t *testing.T) {
	mgr := mcp.NewManager()
	if err := mgr.Register("fs", &fakeMCPClient{
		tools: []mcp.Tool{{Name: "boom", InputSchema: map[string]any{"type": "object"}}},
		call:  func(string, map[string]any) (*mcp.Result, error) { return nil, errors.New("server error") },
	}); err != nil {
		t.Fatal(err)
	}
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(),
		MCPManager: mgr, Policy: permissivePolicy(),
	})
	if _, err := exec.executeMCPTool(t.Context(), llm.ToolCallResponse{Name: "mcp_fs_boom"}); err == nil || !strings.Contains(err.Error(), "server error") {
		t.Errorf("got %v, want the server error", err)
	}
}
