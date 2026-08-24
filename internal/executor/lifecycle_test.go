package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

	e := mustNewExecutor(t, wf, m, nil, nil)
	if got, _ := e.resolver.Model("anything"); got != m {
		t.Error("single resolver must return the default model")
	}

	e = mustNew(t, Config{Workflow: wf, Resolver: fakeResolver{models: map[string]llm.Model{"": m}}})
	if e.model != m {
		t.Error("model must be derived from the resolver's default profile")
	}

	e = mustNew(t, Config{Workflow: wf})
	if e.model != nil || e.resolver != nil {
		t.Error("no model and no resolver stays nil")
	}
}

func TestSpawnAgentWithPrompt_ProfileResolution(t *testing.T) {
	fast := llmmock.New()
	fast.SetResponse("fast answer")
	wf := &agentfile.Workflow{Name: "x"}

	exec := mustNew(t, Config{Workflow: wf, Model: llmmock.New(), Resolver: fakeResolver{models: map[string]llm.Model{"fast": fast}}})
	out, _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "task", nil, "fast", nil, false, 0)
	if err != nil || out != "fast answer" {
		t.Fatalf("got %q %v", out, err)
	}

	exec = mustNew(t, Config{Workflow: wf, Model: llmmock.New(), Resolver: fakeResolver{err: errors.New("unknown profile")}})
	if _, _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "task", nil, "slow", nil, false, 0); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("expected resolver error, got %v", err)
	}
}

func TestRunOptions_ScopedToOneRun(t *testing.T) {
	sess := &session.Session{}
	buf := NewInterruptBuffer()
	var published []string

	var exec *Executor
	model := modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		// Mid-run the executor sees this run's options.
		if exec.interruptBuffer != buf {
			t.Error("interrupt buffer not visible during the run")
		}
		exec.publishToDiscuss("g", "")
		exec.publishToDiscuss("g", "c")
		return &llm.ChatResponse{Content: "done"}, nil
	})
	exec = mustNew(t, Config{Workflow: oneGoalWorkflow(), Model: model, Session: sess, PersistentSession: true})

	if exec.Registry() != nil {
		t.Error("expected nil registry")
	}
	if _, err := exec.Run(t.Context(), RunOptions{
		Interrupts: buf,
		Discuss:    func(goal, content string) { published = append(published, goal+":"+content) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// "g:" is absent: empty content never publishes. "work:done" is the
	// goal's own answer, published by the executor.
	if len(published) != 2 || published[0] != "g:c" || published[1] != "work:done" {
		t.Errorf("published = %v, want [g:c work:done]", published)
	}
	// Options do not leak into the next run.
	if exec.interruptBuffer != nil || exec.discussPublisher != nil {
		t.Error("run options outlived the run")
	}
	exec.publishToDiscuss("g", "d")
	if len(published) != 2 {
		t.Error("cleared publisher must not fire")
	}

	exec.flushSession()
	exec.closeSession()

	// Nil-session variants are no-ops.
	none := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	none.flushSession()
	none.closeSession()
}

func TestExtractAndStoreObservations(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x"}
	newExec := func(ex ObservationExtractor, st ObservationStore) *Executor {
		return mustNew(t, Config{Workflow: wf, Model: llmmock.New(), ObservationExtractor: ex, ObservationStore: st})
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

// TestExtractAndStoreObservations_EmitsSessionEvent guards #3: agentkit's
// extractor previously silently returned empty and, separately, the agent
// side never emitted a session event for the attempt at all — so
// "Observations: enabled" in run.log was never backed by any JSONL
// evidence across 49 real runs. A successful extraction, an empty
// extraction, and a failed extraction must each produce one observation
// event with the outcome distinguishable from the JSONL alone.
func TestExtractAndStoreObservations_EmitsSessionEvent(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x"}
	newExec := func(t *testing.T, ex ObservationExtractor, st ObservationStore) (*Executor, *session.Session) {
		sess := &session.Session{}
		e := mustNew(t, Config{Workflow: wf, Model: llmmock.New(), Session: sess, ObservationExtractor: ex, ObservationStore: st})
		return e, sess
	}

	// Successful extraction with findings.
	store := &fakeStore{done: make(chan string, 1)}
	exec, sess := newExec(t, &fakeExtractor{}, store)
	exec.extractAndStoreObservations(context.Background(), "goal1", "GOAL", "output")
	<-store.done
	waitForEvent(t, sess, session.EventObservation)
	obs := eventsOfType(sess, session.EventObservation)[0]
	if obs.Meta.ObservationCount == 0 {
		t.Errorf("expected non-zero observation count, got %+v", obs.Meta)
	}
	if obs.Success == nil || !*obs.Success {
		t.Errorf("successful extraction logged success=%v, want true", obs.Success)
	}

	// Empty extraction still logs an event (count=0, not an error).
	exec, sess = newExec(t, &fakeExtractor{}, store)
	exec.extractAndStoreObservations(context.Background(), "goal1", "GOAL", "")
	waitForEvent(t, sess, session.EventObservation)
	obs = eventsOfType(sess, session.EventObservation)[0]
	if obs.Meta.ObservationCount != 0 || obs.Meta.ObservationStoreError != "" {
		t.Errorf("empty extraction: %+v", obs.Meta)
	}

	// Extractor error is logged, not silently dropped.
	exec, sess = newExec(t, &fakeExtractor{err: errors.New("boom")}, store)
	exec.extractAndStoreObservations(context.Background(), "goal1", "GOAL", "output")
	waitForEvent(t, sess, session.EventObservation)
	obs = eventsOfType(sess, session.EventObservation)[0]
	if obs.Meta.ObservationStoreError == "" || obs.Success == nil || *obs.Success {
		t.Errorf("extractor error not reflected: %+v success=%v", obs.Meta, obs.Success)
	}
}

// waitForEvent polls sess for at least one event of typ, failing the test
// if none appears (extractAndStoreObservations logs from a background
// goroutine).
func waitForEvent(t *testing.T, sess *session.Session, typ string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(eventsOfType(sess, typ)) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s event within timeout", typ)
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
	exec := mustNew(t, Config{
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
	res, err := exec.Run(context.Background(), RunOptions{})
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
	res, err := exec.Run(context.Background(), RunOptions{})
	if err != nil || res.Outputs["work"] != "corrected" || executions != 2 {
		t.Fatalf("reorient: %+v %v executions=%d", res, err, executions)
	}

	exec, _ = newSupervisedExecutor(t, supervisedWorkflow(), model, fakeSupervisor{supervise: true, verdict: "PAUSE"})
	if _, err := exec.Run(context.Background(), RunOptions{}); err == nil || !strings.Contains(err.Error(), "paused") {
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
	if _, err := exec.Run(context.Background(), RunOptions{}); err != nil {
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
	res, err := exec.Run(context.Background(), RunOptions{})
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
	if _, err := exec.Run(context.Background(), RunOptions{}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("expected pause, got %v", err)
	}
	exec, _ = newSupervisedExecutor(t, wf, model, fakeSupervisor{supervise: true, err: errors.New("sup down")})
	if _, err := exec.Run(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("supervisor error must not fail the goal: %v", err)
	}

	// Unknown agent.
	bad := &agentfile.Workflow{Name: "bad", Steps: wf.Steps, Goals: []agentfile.Goal{{Name: "review", UsingAgent: []string{"ghost"}}}}
	exec = mustNewExecutor(t, bad, model, nil, nil)
	if _, err := exec.Run(context.Background(), RunOptions{}); err == nil || !strings.Contains(err.Error(), "agent not found") {
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
	if _, _, err := exec.spawnAgentWithPrompt(context.Background(), "r", "sys", "t", nil, "", nil, true, 0); err == nil || !strings.Contains(err.Error(), "paused by supervisor") {
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
	exec := mustNew(t, Config{Workflow: wf, Model: looping, Registry: reg, Policy: permissivePolicy()})
	out, err := exec.spawnDynamicAgent(context.Background(), "r", "t", nil)
	if err != nil || !strings.Contains(out, "maximum turn limit") {
		t.Fatalf("got %q %v", out, err)
	}

	exec = mustNew(t, Config{Workflow: wf, Model: modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
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
	exec := mustNew(t, Config{Workflow: wf, Model: modelFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return nil, errors.New("llm down")
	}), WorkspaceContext: "WORKSPACE: here"})
	if res, err := exec.Run(context.Background(), RunOptions{}); err == nil || res.Status != StatusFailed {
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
	exec = mustNew(t, Config{Workflow: wf, Model: model})
	res, err := exec.Run(context.Background(), RunOptions{Interrupts: buf})
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
	exec := mustNewExecutor(t, wf, llmmock.New(), nil, nil)
	if res, err := exec.Run(context.Background(), RunOptions{}); err == nil || res.Status != StatusFailed {
		t.Fatalf("expected preflight failure, got %+v %v", res, err)
	}
}

func TestGoalOutcomeAndMissingGoal(t *testing.T) {
	wf := &agentfile.Workflow{Name: "x", Goals: []agentfile.Goal{{Name: "g", Outcome: "Do $thing"}, {Name: "empty"}}}
	exec := mustNewExecutor(t, wf, llmmock.New(), nil, nil)
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

// A cut that lands mid-rune must back off to the previous rune boundary
// rather than splitting a multi-byte UTF-8 character into invalid bytes.
func TestTruncateForLog_DoesNotSplitUTF8Rune(t *testing.T) {
	s := strings.Repeat("\u00e9", 5) // 'é', 2 bytes each; maxLen=3 lands mid-rune
	got := truncateForLog(s, 3)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateForLog(%q, 3) = %q, not valid UTF-8", s, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("got %q, want truncation suffix", got)
	}
}
