package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
)

func newLoggingExecutor(t *testing.T, debug bool) (*Executor, *session.Session, *bytes.Buffer) {
	t.Helper()
	sess := &session.Session{}
	var buf bytes.Buffer
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "log"},
		Model:    llmmock.New(),
		Session:  sess,
		Debug:    debug,
		Logger:   slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return exec, sess, &buf
}

// Session events must keep their wire schema: these are the JSON keys the
// replay tooling reads. Pin the ones produced by the executor's security and
// phase logging.
func TestSessionEventSchema(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, true)
	exec.currentGoal = "g"
	exec.logSecurityStatic("bash", "b0001", []string{"b0001"}, false, []string{"tool:bash"}, "", []session.TaintNode{{BlockID: "b0001", Trust: "untrusted", Source: "s", TaintedBy: []session.TaintNode{{BlockID: "b0000", Depth: 1}}}})
	exec.logSecurityTriage("bash", "b0001", true, "triage", 12, 3, 4, "")
	exec.logSecuritySupervisor("bash", "b0001", "DENY", "why", "supervisor", 34, 5, 6)
	exec.logSecurityDecision("bash", "deny", "why", "", "static→triage→supervisor")
	exec.logSecurityBlock("b0001", "untrusted", "data", "tool:web_fetch", "<block/>", 4.2)
	exec.LogBashSecurity("go test ./...", "llm", true, "safe, read-only", 42, 7, 8)
	exec.logObservation("GOAL:g", []string{"f1"}, []string{"i1"}, nil, "")
	exec.logSubAgentStart("r", "r", "reasoning-heavy", "deepseek-v4-pro:cloud", "task", nil)
	exec.logSubAgentEnd("r", "r", "reasoning-heavy", "deepseek-v4-pro:cloud", "out", 5, errors.New("boom"))
	ctx := withAgentIdentity(context.Background(), "w", "r")
	exec.logToolResult(ctx, "read", nil, "c1", "some result text", nil, time.Millisecond)
	exec.logLLMCall(ctx, session.EventAssistant, nil, &llm.ChatResponse{Content: "hi there", Model: "m"}, time.Millisecond)

	wantKeys := map[string][]string{
		session.EventSecurityStatic:     {"check", "block_id", "related_blocks", "flags", "taint_lineage"},
		session.EventSecurityTriage:     {"check", "block_id", "suspicious", "model", "latency_ms", "tokens_in", "tokens_out"},
		session.EventSecuritySupervisor: {"supervisor_type", "check", "block_id", "verdict", "reason", "model", "tokens_in", "tokens_out"},
		session.EventSecurityDecision:   {"action", "reason", "check_path"},
		session.EventSecurityBlock:      {"block_id", "trust", "block_type", "source", "entropy"},
		// #1: bash_security must carry the command and reason regardless of
		// debug mode (source doubles as the command, check as the stage).
		session.EventBashSecurity: {"check", "action", "reason", "source", "latency_ms", "tokens_in", "tokens_out"},
		// #3: observation events must exist at all and carry counts.
		session.EventObservation: {"obs_source", "obs_count", "obs_findings", "obs_insights"},
		// #7: subagent_start/end must carry both profile and resolved model.
		session.EventSubAgentStart: {"subagent_name", "subagent_role", "subagent_profile", "subagent_model"},
		// #7, #8: subagent_end must carry resolved model, and its error
		// must survive persistence via meta.error (Event.Error is shadowed
		// on the wire by jsonlRecord.Error of the same JSON name).
		session.EventSubAgentEnd: {"subagent_model", "subagent_profile", "error"},
		// #2, #9: tool_result/assistant must carry size+hash even without
		// full content, so a run stays diagnosable without --debug.
		session.EventToolResult: {"content_size", "content_hash"},
		session.EventAssistant:  {"content_size", "content_hash"},
	}
	for _, ev := range sess.Events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Meta map[string]any `json:"meta"`
		}
		json.Unmarshal(raw, &decoded)
		for _, k := range wantKeys[ev.Type] {
			if _, ok := decoded.Meta[k]; !ok {
				t.Errorf("%s: missing meta key %q in %s", ev.Type, k, raw)
			}
		}
		if strings.Contains(string(raw), "event_seq") {
			t.Errorf("%s: event_seq must be omitted when empty: %s", ev.Type, raw)
		}
	}
	static := eventsOfType(sess, session.EventSecurityStatic)[0]
	raw, _ := json.Marshal(static.Meta.TaintLineage[0].TaintedBy[0])
	if !strings.Contains(string(raw), `"depth":1`) {
		t.Errorf("expected depth in nested taint node: %s", raw)
	}
}

func TestLogBashSecurity(t *testing.T) {
	exec, sess, buf := newLoggingExecutor(t, false)
	exec.LogBashSecurity("rm -rf /", "deterministic", false, "banned", 3, 1, 2)
	exec.LogBashSecurity("ls", "llm", true, "", 9, 10, 20)

	evs := eventsOfType(sess, session.EventBashSecurity)
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	deny := evs[0].Meta
	if deny.Action != "deny" || deny.Pass || deny.Reason != "banned" || deny.Source != "rm -rf /" || deny.TokensIn != 1 || deny.TokensOut != 2 || deny.CheckName != "deterministic" {
		t.Errorf("unexpected deny meta %+v", deny)
	}
	if !strings.Contains(evs[0].Content, "[deterministic] BLOCK: rm -rf / | reason: banned") {
		t.Errorf("unexpected content %q", evs[0].Content)
	}
	if evs[1].Meta.Action != "allow" || !strings.Contains(evs[1].Content, "ALLOW: ls") {
		t.Errorf("unexpected allow event %+v", evs[1])
	}
	if !strings.Contains(buf.String(), "bash security check") {
		t.Error("expected structured log line")
	}

	// Without a session the call is a no-op.
	noSess := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	noSess.LogBashSecurity("ls", "llm", true, "", 0, 0, 0)
}

func TestLogHelpers_DebugContent(t *testing.T) {
	exec, sess, buf := newLoggingExecutor(t, true)
	ctx := withAgentIdentity(context.Background(), "worker", "researcher")

	exec.logToolResult(ctx, "read", map[string]any{"path": "x"}, "c1", "out", errors.New("bad"), time.Millisecond)
	exec.logLLMCall(ctx, session.EventAssistant, []llm.Message{{Role: "user", Content: "hi"}}, &llm.ChatResponse{Content: "yo", Model: "m", InputTokens: 1, OutputTokens: 2, Thinking: "t"}, time.Millisecond)
	exec.logGoalStart("g")
	exec.logGoalEnd("g", strings.Repeat("x", 2500), GoalOutcome{Outcome: OutcomeOK})
	exec.logPhaseCommit("g", "plan", "high", 5)
	exec.logPhaseExecute("g", "complete", 6)
	exec.logPhaseReconcile("g", "s", []string{"t1"}, true, 7)
	exec.logPhaseSupervise("g", "s", "REORIENT", "fix it", true, 8)
	exec.logCheckpoint("pre", "g", "s", "cp1")
	exec.logSubAgentStart("r", "r", "fast", "deepseek-v4-pro:cloud", "task", map[string]string{"k": "v"})
	exec.logSubAgentEnd("r", "r", "fast", "deepseek-v4-pro:cloud", "out", 9, errors.New("sub failed"))

	byType := map[string]session.Event{}
	for _, ev := range sess.Events {
		byType[ev.Type] = ev
	}
	if ev := byType[session.EventToolResult]; ev.Content != "out" || ev.Error != "bad" || ev.Agent != "worker" || ev.AgentRole != "researcher" {
		t.Errorf("tool result: %+v", ev)
	}
	if ev := byType[session.EventAssistant]; ev.Content != "yo" || ev.Meta.Thinking != "t" || ev.Meta.TokensIn != 1 || !strings.Contains(ev.Meta.Prompt, "hi") {
		t.Errorf("llm call: %+v", ev)
	}
	if ev := byType[session.EventGoalEnd]; !strings.HasSuffix(ev.Content, "... (truncated)") {
		t.Errorf("goal end not truncated: %d", len(ev.Content))
	}
	if ev := byType[session.EventPhaseCommit]; ev.Meta.Commitment != "plan" || ev.Meta.Confidence != "high" {
		t.Errorf("commit: %+v", ev.Meta)
	}
	if ev := byType[session.EventPhaseExecute]; ev.Meta.Result != "complete" {
		t.Errorf("execute: %+v", ev.Meta)
	}
	if ev := byType[session.EventPhaseReconcile]; !ev.Meta.Escalate || len(ev.Meta.Triggers) != 1 {
		t.Errorf("reconcile: %+v", ev.Meta)
	}
	if ev := byType[session.EventPhaseSupervise]; ev.Meta.Verdict != "REORIENT" || ev.Meta.Guidance != "fix it" || !ev.Meta.HumanRequired {
		t.Errorf("supervise: %+v", ev.Meta)
	}
	if ev := byType[session.EventCheckpoint]; ev.Meta.CheckpointID != "cp1" || ev.Step != "s" {
		t.Errorf("checkpoint: %+v", ev)
	}
	if ev := byType[session.EventSubAgentStart]; ev.Meta.SubAgentTask != "task" || ev.Meta.SubAgentInputs["k"] != "v" || ev.Meta.SubAgentProfile != "fast" || ev.Meta.SubAgentModel != "deepseek-v4-pro:cloud" {
		t.Errorf("subagent start: %+v", ev.Meta)
	}
	if ev := byType[session.EventSubAgentEnd]; ev.Error != "sub failed" || *ev.Success || ev.Meta.SubAgentOutput != "out" || ev.Meta.SubAgentModel != "deepseek-v4-pro:cloud" || ev.Meta.Error != "sub failed" {
		t.Errorf("subagent end: %+v", ev)
	}
	if !strings.Contains(buf.String(), "tool_error") {
		t.Error("expected tool_error log line")
	}

	// Non-debug policy (#2, #9): the FULL prompt/response/tool-output/
	// sub-agent-output fields (meta.Prompt/Response/SubAgentOutput, and
	// Event.Content beyond previewLen) are still withheld outside --debug
	// (PII protection, unchanged). What changed: every such event now
	// always carries a truncated preview (<=previewLen bytes) plus the full
	// content's byte size and hash, so a run stays diagnosable without a
	// --debug rerun. A short string like "secret" is entirely within
	// previewLen, so it appears in the preview by design — the guarantee
	// is "nothing beyond previewLen bytes leaks", not "nothing leaks".
	quiet, qsess, qbuf := newLoggingExecutor(t, false)
	longSecret := "TOP-SECRET:" + strings.Repeat("x", previewLen+50)
	quiet.logToolResult(ctx, "read", nil, "c1", longSecret, nil, time.Millisecond)
	quiet.logLLMCall(ctx, session.EventAssistant, nil, &llm.ChatResponse{Content: longSecret}, 0)
	quiet.logGoalEnd("g", longSecret, GoalOutcome{Outcome: OutcomeOK})
	quiet.logSubAgentEnd("r", "r", "", "", longSecret, 0, nil)
	for _, ev := range qsess.Events {
		if len(ev.Content) > previewLen+len("...") {
			t.Errorf("content not truncated to preview without debug: %+v", ev)
		}
		if ev.Meta != nil && (ev.Meta.Response == longSecret || ev.Meta.SubAgentOutput == longSecret || ev.Meta.Result == longSecret) {
			t.Errorf("full content leaked into debug-only meta field without debug: %+v", ev)
		}
	}
	if ev := byTypeIn(qsess, session.EventToolResult); ev.Meta == nil || ev.Meta.ContentSize != len(longSecret) || ev.Meta.ContentHash == "" {
		t.Errorf("tool_result missing content size/hash without debug: %+v", ev.Meta)
	}
	if ev := byTypeIn(qsess, session.EventAssistant); ev.Meta == nil || ev.Meta.ContentSize != len(longSecret) || ev.Meta.ContentHash == "" {
		t.Errorf("assistant missing content size/hash without debug: %+v", ev.Meta)
	}
	if !strings.Contains(qbuf.String(), "tool_result") {
		t.Error("expected tool_result debug log line")
	}
}

// byTypeIn returns the first event of the given type in sess, or the zero
// Event if none.
func byTypeIn(sess *session.Session, typ string) session.Event {
	for _, ev := range sess.Events {
		if ev.Type == typ {
			return ev
		}
	}
	return session.Event{}
}

func TestLogHelpers_NoSessionNoop(t *testing.T) {
	exec := mustNewExecutor(t, &agentfile.Workflow{Name: "x"}, llmmock.New(), nil, nil)
	ctx := context.Background()
	exec.logEvent("t", "c")
	exec.logToolCall(ctx, "t", nil)
	exec.logToolResult(ctx, "t", nil, "", "", nil, 0)
	exec.logLLMCall(ctx, "t", nil, &llm.ChatResponse{}, 0)
	exec.logGoalStart("g")
	exec.logGoalEnd("g", "", GoalOutcome{Outcome: OutcomeOK})
	exec.logPhaseCommit("g", "", "", 0)
	exec.logPhaseExecute("g", "", 0)
	exec.logPhaseReconcile("g", "", nil, false, 0)
	exec.logPhaseSupervise("g", "", "", "", false, 0)
	exec.logCheckpoint("pre", "g", "", "")
	exec.logSecurityBlock("", "", "", "", "", 0)
	exec.logSecurityStatic("", "", nil, true, nil, "", nil)
	exec.logSecurityTriage("", "", false, "", 0, 0, 0, "")
	exec.logSecuritySupervisor("", "", "", "", "", 0, 0, 0)
	exec.logSecurityDecision("", "", "", "", "")
	exec.logSubAgentStart("", "", "", "", "", nil)
	exec.logSubAgentEnd("", "", "", "", "", 0, nil)
}

func TestTracingSpans(t *testing.T) {
	exec := mustNew(t, Config{Workflow: &agentfile.Workflow{Name: "x"}, Model: llmmock.New(), Debug: true})
	ctx := context.Background()
	_, span := exec.startWorkflowSpan(ctx, "wf")
	exec.endWorkflowSpan(span, "failed", errors.New("e"))
	_, span = exec.startGoalSpan(ctx, "g", true)
	exec.endGoalSpan(span, "output", errors.New("e"))
	_, span = exec.startPhaseSpan(ctx, "COMMIT", "g")
	exec.endPhaseSpan(span, map[string]string{"k": "v"}, errors.New("e"))
	_, span = exec.startSubAgentSpan(ctx, "r", "m")
	exec.endSubAgentSpan(span, "output", nil)
	exec.debug = false
	_, span = exec.startGoalSpan(ctx, "g", false)
	exec.endGoalSpan(span, "output", nil)
	_, span = exec.startSubAgentSpan(ctx, "r", "m")
	exec.endSubAgentSpan(span, "output", errors.New("e"))
}

// TestLogToolResult_ErrorSurvivesInMeta guards against Event.Error being
// silently dropped: jsonlRecord's footer-level "error" field shadows the
// embedded Event.Error on the wire (see EventMeta.Error's doc comment), so
// a failed tool's error text must also live in meta.error to survive a
// round trip through the session log.
func TestLogToolResult_ErrorSurvivesInMeta(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, false)
	ctx := context.Background()

	exec.logToolResult(ctx, "bash", map[string]any{"command": "false"}, "c1", "", errors.New("exit status 1"), time.Millisecond)

	var ev session.Event
	for _, e := range sess.Events {
		if e.Type == session.EventToolResult {
			ev = e
		}
	}
	if ev.Meta == nil || ev.Meta.Error != "exit status 1" {
		t.Fatalf("meta.error = %+v, want %q", ev.Meta, "exit status 1")
	}
}

// TestLogToolResult_ResultRecordedInMeta checks the (truncated) result text
// lands in meta.result in debug mode, for both success and failure — the
// same PII rule that gates Content also gates meta.result.
func TestLogToolResult_ResultRecordedInMeta(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, true)
	ctx := context.Background()

	exec.logToolResult(ctx, "read", map[string]any{"path": "x"}, "c1", strings.Repeat("y", 600), nil, time.Millisecond)

	var ev session.Event
	for _, e := range sess.Events {
		if e.Type == session.EventToolResult {
			ev = e
		}
	}
	if ev.Meta == nil || len(ev.Meta.Result) != 503 || !strings.HasSuffix(ev.Meta.Result, "...") {
		t.Fatalf("meta.result = %d bytes, want 500 + \"...\" (truncated)", len(ev.Meta.Result))
	}
}

// TestLogToolResult_ResultWithheldWithoutDebug checks meta.result is empty
// outside debug mode, even though meta.error (checked separately in
// TestLogToolResult_ErrorSurvivesInMeta) is always populated on failure.
func TestLogToolResult_ResultWithheldWithoutDebug(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, false)
	ctx := context.Background()

	exec.logToolResult(ctx, "read", map[string]any{"path": "x"}, "c1", "secret output", errors.New("bad"), time.Millisecond)

	var ev session.Event
	for _, e := range sess.Events {
		if e.Type == session.EventToolResult {
			ev = e
		}
	}
	if ev.Meta == nil || ev.Meta.Result != "" {
		t.Fatalf("meta.result = %q, want empty without debug", ev.Meta.Result)
	}
	if ev.Meta.Error != "bad" {
		t.Fatalf("meta.error = %q, want %q even without debug", ev.Meta.Error, "bad")
	}
}

// TestLogLLMCall_StopReasonAlwaysLogged checks that stop_reason and
// thinking_chars land in meta.* on every assistant event, not just in
// --debug mode: a headless caller needs these to diagnose a
// stop_reason=="length"-with-empty-content turn (P0 #1) without having to
// re-run with --debug (which would also leak prompt/response content).
func TestLogLLMCall_StopReasonAlwaysLogged(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, false)
	ctx := context.Background()

	exec.logLLMCall(ctx, session.EventAssistant, []llm.Message{{Role: "user", Content: "hi"}},
		&llm.ChatResponse{Content: "", Model: "m", StopReason: "length", Thinking: "long reasoning trace"},
		time.Millisecond)

	var ev session.Event
	for _, e := range sess.Events {
		if e.Type == session.EventAssistant {
			ev = e
		}
	}
	if ev.Meta == nil {
		t.Fatal("no meta on assistant event")
	}
	if ev.Meta.StopReason != "length" {
		t.Errorf("meta.stop_reason = %q, want %q", ev.Meta.StopReason, "length")
	}
	if ev.Meta.ThinkingChars != len("long reasoning trace") {
		t.Errorf("meta.thinking_chars = %d, want %d", ev.Meta.ThinkingChars, len("long reasoning trace"))
	}
	// Debug-only fields stay withheld.
	if ev.Meta.Response != "" || ev.Meta.Prompt != "" || ev.Meta.Thinking != "" {
		t.Errorf("debug-only fields leaked without debug: %+v", ev.Meta)
	}
}

// TestSubAgentStartEndArePaired guards the regression where CONVERGE-pipeline
// sub-agents logged a subagent_start that never got a matching subagent_end,
// because only the GOAL fan-out path called logSubAgentEnd.
func TestSubAgentStartEndArePaired(t *testing.T) {
	src, err := os.ReadFile("subagent.go")
	if err != nil {
		t.Fatalf("read subagent.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "e.logSubAgentStart(") {
		t.Fatal("logSubAgentStart no longer called from spawnAgentWithPrompt")
	}
	if !strings.Contains(body, "e.logSubAgentEnd(") {
		t.Error("spawnAgentWithPrompt must log subagent_end on every return path, " +
			"otherwise non-fan-out paths (CONVERGE pipeline) emit unmatched start events")
	}
}

// TestLogGoalEnd_SuccessHasNoError guards against a successful goal logging its
// convergence reason in the Error field, which makes log consumers read a
// healthy goal as a failure.
func TestLogGoalEnd_SuccessHasNoError(t *testing.T) {
	exec, sess, _ := newLoggingExecutor(t, false)
	exec.logGoalEnd("g", "out", GoalOutcome{Outcome: OutcomeOK, Reason: "all checks passed"})
	exec.logGoalEnd("h", "out", GoalOutcome{Outcome: OutcomeBudgetExhausted, Reason: "ran out"})

	evs := sess.Events
	if got := evs[0].Meta.Error; got != "" {
		t.Errorf("successful goal logged Error=%q, want empty", got)
	}
	if got := evs[0].Meta.Reason; got != "all checks passed" {
		t.Errorf("successful goal Reason=%q, want it preserved", got)
	}
	if got := evs[1].Meta.Error; got != "ran out" {
		t.Errorf("failed goal Error=%q, want the reason", got)
	}
}

// TestSpawnDynamicAgent_EmitsPairedSubAgentEvents guards #5: the spawn_agents
// tool binds directly to spawnDynamicAgent (cfg.SpawnBinder.Bind), which
// previously fired hooks only and never wrote subagent_start/end session
// events at all — so neither dynamic spawn_agents calls nor static
// `RUN ... USING` goals that go through it left any session record.
func TestSpawnDynamicAgent_EmitsPairedSubAgentEvents(t *testing.T) {
	sess := &session.Session{}
	model := modelFunc(func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "done", Model: "deepseek-v4-pro:cloud"}, nil
	})
	exec := mustNew(t, Config{
		Workflow: &agentfile.Workflow{Name: "x"},
		Model:    model,
		Session:  sess,
	})

	out, err := exec.spawnDynamicAgent(context.Background(), "researcher", "find things", nil)
	if err != nil {
		t.Fatalf("spawnDynamicAgent: %v", err)
	}
	if out != "done" {
		t.Errorf("output = %q, want %q", out, "done")
	}

	starts := eventsOfType(sess, session.EventSubAgentStart)
	ends := eventsOfType(sess, session.EventSubAgentEnd)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("spawnDynamicAgent must emit paired subagent_start/end; got %d starts, %d ends", len(starts), len(ends))
	}
	if ends[0].Meta.SubAgentModel != "deepseek-v4-pro:cloud" {
		t.Errorf("subagent_end.meta.subagent_model = %q, want the resolved model", ends[0].Meta.SubAgentModel)
	}
	if ends[0].Success == nil || !*ends[0].Success {
		t.Errorf("subagent_end.success = %v, want true", ends[0].Success)
	}
}

// TestLogToolCall_CorrIDUnique guards #7 (numbered #6/#7 depending on
// section): fmt.Sprintf("tool-%d", time.Now().UnixNano()) collided when
// parallel tool calls landed in the same nanosecond tick. corr_id must be
// distinct across concurrent calls regardless of clock resolution.
func TestLogToolCall_CorrIDUnique(t *testing.T) {
	exec, _, _ := newLoggingExecutor(t, false)
	ctx := context.Background()

	const n = 200
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = exec.logToolCall(ctx, "read", nil)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate corr_id %q", id)
		}
		seen[id] = true
	}
}
