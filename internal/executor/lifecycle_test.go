package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/memory"
)

// fakeResolver returns a model per profile or an error.
type fakeResolver struct {
	models map[string]llm.Model
	err    error
}

func (r fakeResolver) Model(profile string) (llm.Model, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.models[profile], nil
}

// fakeSupervisor drives the supervision pipeline deterministically.
type fakeSupervisor struct {
	supervise bool
	verdict   string
	err       error // returned for GOAL-level supervision only
}

func (s fakeSupervisor) Reconcile(pre *checkpoint.PreCheckpoint, _ *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult {
	return &checkpoint.ReconcileResult{StepID: pre.StepID, Triggers: []string{"test"}, Supervise: s.supervise, Timestamp: time.Now()}
}

func (s fakeSupervisor) Supervise(_ context.Context, req supervision.SuperviseRequest) (*checkpoint.SuperviseResult, error) {
	if s.err != nil && req.Pre.StepType == "GOAL" {
		return nil, s.err
	}
	return &checkpoint.SuperviseResult{StepID: req.Pre.StepID, Verdict: s.verdict, Correction: "do better", Question: "why?", Timestamp: time.Now()}, nil
}

type fakeExtractor struct {
	source string
	err    error
}

func (f *fakeExtractor) Extract(_ context.Context, text string, opts ...memory.ExtractOption) ([]string, []string, []string, error) {
	if f.err != nil {
		return nil, nil, nil, f.err
	}
	if text == "" {
		return nil, nil, nil, nil
	}
	return []string{"f:" + text}, []string{"i"}, nil, nil
}

type fakeStore struct {
	done   chan string
	failed bool
}

func (s *fakeStore) RememberFIL(_ context.Context, findings, insights, lessons []string, source string) ([]string, error) {
	defer func() { s.done <- source }()
	if s.failed {
		return nil, errors.New("store down")
	}
	return []string{"id"}, nil
}

func TestNew_ResolverAndModelDerivation(t *testing.T) {
	m := llmmock.New()
	wf := &agentfile.Workflow{Name: "x"}

	e := NewExecutor(wf, m, nil, nil)
	if got, _ := e.resolver.Model("anything"); got != m {
		t.Error("single resolver must return the default model")
	}

	e = NewExecutorWithFactory(wf, fakeResolver{models: map[string]llm.Model{"": m}}, nil, nil)
	if e.model != m {
		t.Error("model must be derived from the resolver's default profile")
	}

	e = New(Config{Workflow: wf})
	if e.model != nil || e.resolver != nil {
		t.Error("no model and no resolver stays nil")
	}
}

func TestSpawnAgentWithPrompt_ProfileResolution(t *testing.T) {
	fast := llmmock.New()
	fast.SetResponse("fast answer")
	wf := &agentfile.Workflow{Name: "x"}

	exec := New(Config{Workflow: wf, Model: llmmock.New(), Resolver: fakeResolver{models: map[string]llm.Model{"fast": fast}}})
	out, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "task", nil, "fast", nil, false)
	if err != nil || out != "fast answer" {
		t.Fatalf("got %q %v", out, err)
	}

	exec = New(Config{Workflow: wf, Model: llmmock.New(), Resolver: fakeResolver{err: errors.New("unknown profile")}})
	if _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "task", nil, "slow", nil, false); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("expected resolver error, got %v", err)
	}
}

func TestSettersAndAccessors(t *testing.T) {
	sess := &session.Session{}
	exec := New(Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), Session: sess})

	if exec.Registry() != nil {
		t.Error("expected nil registry")
	}
	buf := NewInterruptBuffer()
	exec.SetInterruptBuffer(buf)
	if exec.InterruptBuffer() != buf {
		t.Error("interrupt buffer not set")
	}

	var published string
	exec.SetDiscussPublisher(func(goal, content string) { published = goal + ":" + content })
	exec.publishToDiscuss("g", "")
	if published != "" {
		t.Error("empty content must not publish")
	}
	exec.publishToDiscuss("g", "c")
	if published != "g:c" {
		t.Errorf("got %q", published)
	}
	exec.ClearDiscussPublisher()
	exec.publishToDiscuss("g", "d")
	if published != "g:c" {
		t.Error("cleared publisher must not fire")
	}

	var seen []string
	exec.SetEventPublisher(func(ev session.Event) { seen = append(seen, ev.Type) })
	exec.logEvent("custom", "x")
	exec.ClearEventPublisher()
	exec.logEvent("custom2", "x")
	if len(seen) != 1 || seen[0] != "custom" {
		t.Errorf("event publisher: %v", seen)
	}

	exec.SetPersistentSession(true)
	exec.flushSession()
	exec.closeSession()

	// Nil-session variants are no-ops.
	none := NewExecutor(&agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	none.SetEventPublisher(nil)
	none.ClearEventPublisher()
	none.flushSession()
	none.closeSession()
}

func TestExtractAndStoreObservations(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x"}
	newExec := func(ex ObservationExtractor, st ObservationStore) *Executor {
		return New(Config{Workflow: wf, Model: llmmock.New(), ObservationExtractor: ex, ObservationStore: st})
	}

	store := &fakeStore{done: make(chan string, 1)}
	newExec(&fakeExtractor{}, store).extractAndStoreObservations(context.Background(), "goal1", "GOAL", "output")
	select {
	case src := <-store.done:
		if src != "GOAL:goal1" {
			t.Errorf("unexpected source %q", src)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observation never stored")
	}

	// Nothing extracted, or extractor error => nothing stored.
	newExec(&fakeExtractor{}, store).extractAndStoreObservations(context.Background(), "goal1", "GOAL", "")
	newExec(&fakeExtractor{err: errors.New("no")}, store).extractAndStoreObservations(context.Background(), "goal1", "GOAL", "output")
	select {
	case src := <-store.done:
		t.Fatalf("unexpected store call for %q", src)
	case <-time.After(50 * time.Millisecond):
	}

	// Store failure is logged, not fatal.
	failing := &fakeStore{done: make(chan string, 1), failed: true}
	newExec(&fakeExtractor{}, failing).extractAndStoreObservations(context.Background(), "r", "AGENT", "output")
	<-failing.done

	// Missing extractor or store disables the seam.
	newExec(&fakeExtractor{}, nil).extractAndStoreObservations(context.Background(), "r", "AGENT", "output")
}

// Real memory types satisfy the observation seam without adapters.
func TestObservationSeam_MemoryTypes(t *testing.T) {
	var _ ObservationExtractor = memory.NewExtractor(llmmock.New())
	var _ ObservationStore = memory.NewInMemoryStore()
}

func supervisedWorkflow() *agentfile.Workflow {
	return &agentfile.Workflow{
		Name:       "sup",
		Supervised: true,
		Steps:      []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"work"}}},
		Goals:      []agentfile.Goal{{Name: "work", Outcome: "Do the work"}},
	}
}

func newSupervisedExecutor(t *testing.T, wf *agentfile.Workflow, model llm.Model, sup supervision.Supervisor) (*Executor, *session.Session) {
	t.Helper()
	store, err := checkpoint.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{}
	exec := New(Config{
		Workflow: wf, Model: model, Session: sess, Debug: true,
		CheckpointStore: store, Supervisor: sup,
	})
	return exec, sess
}

func TestSupervisedGoal_CommitExecuteReconcile(t *testing.T) {
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"interpretation":"do it","approach":"fast","confidence":"high","tools_planned":["read"]}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: `{"met_commitment":true,"deviations":[]}`}, nil
		}
		return &llm.ChatResponse{Content: "done"}, nil
	})
	exec, sess := newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "CONTINUE"})
	res, err := exec.Run(context.Background(), nil)
	if err != nil || res.Outputs["work"] != "done" {
		t.Fatalf("got %+v %v", res, err)
	}
	commit := eventsOfType(sess, session.EventPhaseCommit)
	if len(commit) != 1 || commit[0].Meta.Confidence != "high" || commit[0].Meta.Commitment != "do it" {
		t.Errorf("commit event: %+v", commit)
	}
	if len(eventsOfType(sess, session.EventPhaseReconcile)) != 1 || len(eventsOfType(sess, session.EventPhaseSupervise)) != 1 {
		t.Error("expected reconcile and supervise events")
	}
	if n := len(eventsOfType(sess, session.EventCheckpoint)); n != 3 {
		t.Errorf("expected pre, post and supervise checkpoints, got %d", n)
	}
}

func TestSupervisedGoal_ReorientAndPause(t *testing.T) {
	var executions int
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: "not json at all"}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: "{broken json"}, nil
		}
		executions++
		if strings.Contains(user, "do better") {
			return &llm.ChatResponse{Content: "corrected"}, nil
		}
		return &llm.ChatResponse{Content: "first"}, nil
	})
	exec, _ := newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "REORIENT"})
	res, err := exec.Run(context.Background(), nil)
	if err != nil || res.Outputs["work"] != "corrected" || executions != 2 {
		t.Fatalf("reorient: %+v %v executions=%d", res, err, executions)
	}

	exec, _ = newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "PAUSE"})
	if _, err := exec.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("expected pause error, got %v", err)
	}
}

func TestSupervisedGoal_LLMErrorsInPhases(t *testing.T) {
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(user, "declare your intent") || strings.Contains(user, "Assess your work") {
			return nil, errors.New("side model down")
		}
		return &llm.ChatResponse{Content: "done"}, nil
	})
	exec, sess := newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: false})
	if _, err := exec.Run(context.Background(), nil); err != nil {
		t.Fatalf("commit/assess errors must not fail the goal: %v", err)
	}
	commit := eventsOfType(sess, session.EventPhaseCommit)
	if len(commit) != 0 {
		// commit phase logs nothing on LLM error; pre-checkpoint still saved
		t.Errorf("unexpected commit events %+v", commit)
	}
	if len(eventsOfType(sess, session.EventCheckpoint)) != 2 {
		t.Error("expected checkpoints despite LLM errors")
	}
}

func TestSupervisedMultiAgentGoal(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:       "multi",
		Supervised: true,
		Agents:     []agentfile.Agent{{Name: "a1", Prompt: "You are a1"}, {Name: "a2", Prompt: "You are a2"}},
		Steps:      []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"review"}}},
		Goals:      []agentfile.Goal{{Name: "review", Outcome: "Review", UsingAgent: []string{"a1", "a2"}, Outputs: []string{"summary"}}},
	}
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"confidence":"low"}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: `{"met_commitment":false}`}, nil
		case strings.Contains(user, "Synthesize"):
			return &llm.ChatResponse{Content: `{"summary":"both agree"}`}, nil
		}
		return &llm.ChatResponse{Content: "agent says hi"}, nil
	})

	exec, sess := newSupervisedExecutor(t, wf, model, fakeSupervisor{supervise: true, verdict: "CONTINUE"})
	res, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if exec.outputs["summary"] != "both agree" {
		t.Errorf("structured output not parsed: %+v", res.Outputs)
	}
	if len(eventsOfType(sess, session.EventSubAgentEnd)) < 2 {
		t.Error("expected sub-agent end events")
	}
	if len(eventsOfType(sess, session.EventPhaseSupervise)) < 1 {
		t.Error("expected goal-level supervise event")
	}

	// PAUSE fails the run (sub-agents inherit the goal's supervision and
	// pause first); supervisor error is tolerated.
	exec, _ = newSupervisedExecutor(t, wf, model, fakeSupervisor{supervise: true, verdict: "PAUSE"})
	if _, err := exec.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("expected pause, got %v", err)
	}
	exec, _ = newSupervisedExecutor(t, wf, model, fakeSupervisor{supervise: true, err: errors.New("sup down")})
	if _, err := exec.Run(context.Background(), nil); err != nil {
		t.Fatalf("supervisor error must not fail the goal: %v", err)
	}

	// Unknown agent.
	bad := &agentfile.Workflow{Name: "bad", Steps: wf.Steps, Goals: []agentfile.Goal{{Name: "review", UsingAgent: []string{"ghost"}}}}
	exec = NewExecutor(bad, model, nil, nil)
	if _, err := exec.Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("expected agent not found, got %v", err)
	}
}

func TestSupervisedSubAgent(t *testing.T) {
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		switch {
		case strings.Contains(user, "declare your intent"):
			return &llm.ChatResponse{Content: `{"interpretation":"sub","confidence":"medium"}`}, nil
		case strings.Contains(user, "Assess your work"):
			return &llm.ChatResponse{Content: `{"met_commitment":true}`}, nil
		case strings.Contains(user, "do better"):
			return &llm.ChatResponse{Content: "sub corrected"}, nil
		}
		return &llm.ChatResponse{Content: "sub done"}, nil
	})
	exec, sess := newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "REORIENT"})
	exec.currentGoal = "work"
	exec.currentGoalSupervised = true

	out, err := exec.spawnDynamicAgent(context.Background(), "researcher", "find things", []string{"facts"})
	if err != nil || out != "sub corrected" {
		t.Fatalf("got %q %v", out, err)
	}
	if len(eventsOfType(sess, session.EventPhaseCommit)) != 1 {
		t.Error("expected sub-agent commit event")
	}

	exec, _ = newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "PAUSE"})
	exec.currentGoalSupervised = true
	if _, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil); err == nil || !strings.Contains(err.Error(), "paused by supervisor") {
		t.Fatalf("expected pause, got %v", err)
	}
	if _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "t", nil, "", nil, true); err == nil || !strings.Contains(err.Error(), "paused by supervisor") {
		t.Fatalf("expected pause, got %v", err)
	}

	// Sub-agent phase LLM failures degrade gracefully.
	failing := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		user := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(user, "declare your intent") || strings.Contains(user, "Assess your work") {
			return nil, errors.New("down")
		}
		return &llm.ChatResponse{Content: "ok"}, nil
	})
	exec, _ = newSupervisedExecutor(t, supervisedWorkflow(), failing, fakeSupervisor{supervise: false})
	exec.currentGoalSupervised = true
	if out, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil); err != nil || out != "ok" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestSubAgent_TurnLimitAndLLMError(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x"}
	looping := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{ToolCalls: []llm.ToolCallResponse{{ID: "1", Name: "pwd", Args: map[string]any{}}}}, nil
	})
	reg, _ := newTestRegistry(t, t.TempDir())
	exec := New(Config{Workflow: wf, Model: looping, Registry: reg, Policy: permissivePolicy()})
	out, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil)
	if err != nil || !strings.Contains(out, "maximum turn limit") {
		t.Fatalf("got %q %v", out, err)
	}

	exec = New(Config{Workflow: wf, Model: modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, errors.New("llm down")
	})})
	if _, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil); err == nil || !strings.Contains(err.Error(), "sub-agent LLM error") {
		t.Fatalf("expected LLM error, got %v", err)
	}
}

func TestExecutePhase_SkillsInterruptsAndLLMError(t *testing.T) {
	wf := &agentfile.Workflow{
		Name:  "x",
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Work"}},
	}
	exec := New(Config{Workflow: wf, Model: modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, errors.New("llm down")
	}), WorkspaceContext: "WORKSPACE: here"})
	if res, err := exec.Run(context.Background(), nil); err == nil || res.Status != StatusFailed {
		t.Fatalf("expected failure, got %+v %v", res, err)
	}

	// Interrupts delivered before the final answer force another turn.
	buf := NewInterruptBuffer()
	buf.Push(InterruptMessage{From: "peer", Content: "stop and reconsider"})
	var turns int
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		turns++
		last := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(last, "reconsider") {
			return &llm.ChatResponse{Content: "reconsidered"}, nil
		}
		return &llm.ChatResponse{Content: "first"}, nil
	})
	exec = New(Config{Workflow: wf, Model: model, InterruptBuffer: buf})
	res, err := exec.Run(context.Background(), nil)
	if err != nil || res.Outputs["g"] != "reconsidered" || turns != 2 {
		t.Fatalf("got %+v %v turns=%d", res, err, turns)
	}
}

func TestRun_PreFlightFailure(t *testing.T) {
	wf := &agentfile.Workflow{
		Name: "x", Supervised: true, HumanOnly: true,
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, UsingGoals: []string{"g"}}},
		Goals: []agentfile.Goal{{Name: "g", Outcome: "Deploy"}},
	}
	exec := NewExecutor(wf, llmmock.New(), nil, nil)
	if res, err := exec.Run(context.Background(), nil); err == nil || res.Status != StatusFailed {
		t.Fatalf("expected preflight failure, got %+v %v", res, err)
	}
}

func TestGoalOutcomeAndMissingGoal(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x", Goals: []agentfile.Goal{{Name: "g", Outcome: "Do $thing"}, {Name: "empty"}}}
	exec := NewExecutor(wf, llmmock.New(), nil, nil)
	exec.inputs = map[string]string{"thing": "work"}
	if got := exec.goalOutcome("g"); got != "Do work" {
		t.Errorf("got %q", got)
	}
	if got := exec.goalOutcome("empty"); got != "empty" {
		t.Errorf("got %q", got)
	}
	if got := exec.goalOutcome("missing"); got != "missing" {
		t.Errorf("got %q", got)
	}
	if err := exec.ExecuteGoal(context.Background(), "missing", nil); err == nil {
		t.Error("expected error for unknown goal")
	}
}

func TestTruncateForLog(t *testing.T) {
	if got := truncateForLog("abc", 10); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := truncateForLog("abcdef", 3); !strings.HasPrefix(got, "abc") || len(got) <= 3 {
		t.Errorf("got %q", got)
	}
}
