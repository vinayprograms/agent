package replay

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

var update = flag.Bool("update", false, "update golden files")

// goldenSession exercises every event type the replayer formats, so the
// golden files at each verbosity level cover the full switch in
// formatEvent.
func goldenSession() *session.Session {
	t := func(s int) time.Time {
		return time.Date(2026, 1, 1, 12, 0, s, 0, time.UTC)
	}
	events := []session.Event{
		{SeqID: 1, Type: session.EventWorkflowStart, Timestamp: t(0)},
		{SeqID: 2, Type: session.EventGoalStart, Timestamp: t(1), Goal: "build-feature"},
		{SeqID: 3, Type: session.EventSystem, Timestamp: t(2), Goal: "build-feature", Content: "system prompt text"},
		{SeqID: 4, Type: session.EventUser, Timestamp: t(3), Goal: "build-feature", Content: "please build the feature"},
		{SeqID: 5, Type: session.EventAssistant, Timestamp: t(4), Goal: "build-feature", Content: "working on it",
			Meta: &session.EventMeta{Model: "claude-x", TokensIn: 100, TokensOut: 50, LatencyMs: 1200, Thinking: "let me think", Prompt: "full prompt"}},
		{SeqID: 6, Type: session.EventToolCall, Timestamp: t(5), Goal: "build-feature", Tool: "bash",
			CorrelationID: "corr-1", Args: map[string]any{"command": "ls -la"}},
		{SeqID: 7, Type: session.EventToolResult, Timestamp: t(6), Goal: "build-feature", Tool: "bash",
			CorrelationID: "corr-1", DurationMs: 42, Content: "total 0"},
		{SeqID: 8, Type: session.EventToolResult, Timestamp: t(7), Goal: "build-feature", Tool: "bash",
			CorrelationID: "corr-2", DurationMs: 7, Error: "command not found"},
		{SeqID: 9, Type: session.EventWarning, Timestamp: t(8), Goal: "build-feature", Content: "disk space low"},
		{SeqID: 10, Type: session.EventSubAgentStart, Timestamp: t(9), Goal: "build-feature",
			Meta: &session.EventMeta{SubAgentName: "reviewer", SubAgentModel: "claude-y", SubAgentTask: "review the diff"}},
		{SeqID: 11, Type: session.EventSubAgentEnd, Timestamp: t(10), Goal: "build-feature", DurationMs: 500,
			Meta: &session.EventMeta{SubAgentName: "reviewer", SubAgentOutput: "looks good"}},
		{SeqID: 12, Type: session.EventPhaseCommit, Timestamp: t(11), Goal: "build-feature", DurationMs: 10,
			Meta: &session.EventMeta{Confidence: "high", Commitment: "I will edit main.go"}},
		{SeqID: 13, Type: session.EventPhaseExecute, Timestamp: t(12), Goal: "build-feature", DurationMs: 20,
			Meta: &session.EventMeta{Result: "ok"}},
		{SeqID: 14, Type: session.EventPhaseReconcile, Timestamp: t(13), Goal: "build-feature", DurationMs: 5,
			Meta: &session.EventMeta{Escalate: true, Triggers: []string{"diff-too-large"}}},
		{SeqID: 15, Type: session.EventPhaseSupervise, Timestamp: t(14), Goal: "build-feature", DurationMs: 300,
			Meta: &session.EventMeta{Verdict: "CONTINUE", SupervisorType: "execution", Model: "claude-x", Correction: "none",
				Prompt: "supervisor prompt", Response: "supervisor response", Thinking: "supervisor thinking"}},
		{SeqID: 16, Type: session.EventPhaseSupervise, Timestamp: t(15), Goal: "build-feature", DurationMs: 300,
			Meta: &session.EventMeta{Verdict: "REORIENT", SupervisorType: "security"}},
		{SeqID: 17, Type: session.EventSecurityBlock, Timestamp: t(16), Goal: "build-feature", Tool: "web_fetch",
			Meta: &session.EventMeta{BlockID: "b0001", Entropy: 3.2, Source: "http://example.com", RelatedBlocks: []string{"b0000"}}},
		{SeqID: 18, Type: session.EventSecurityStatic, Timestamp: t(17), Goal: "build-feature",
			Meta: &session.EventMeta{Pass: false, Flags: []string{"prompt-injection"}, RelatedBlocks: []string{"b0000", "b0001"},
				TaintLineage: []session.TaintNode{{BlockID: "b0001", Trust: "untrusted", Source: "http://example.com", EventSeq: 17,
					TaintedBy: []session.TaintNode{{BlockID: "b0000", Trust: "trusted", Source: "user"}}}}}},
		{SeqID: 19, Type: session.EventSecurityTriage, Timestamp: t(18), Goal: "build-feature",
			Meta: &session.EventMeta{Suspicious: true, Model: "claude-x", SkipReason: "", Prompt: "triage prompt", Response: "triage response"}},
		{SeqID: 20, Type: session.EventSecuritySupervisor, Timestamp: t(19), Goal: "build-feature",
			Meta: &session.EventMeta{Action: "deny", Model: "claude-x", Reason: "exfiltration attempt", Response: "supervisor said deny"}},
		{SeqID: 21, Type: session.EventSecurityDecision, Timestamp: t(20), Goal: "build-feature",
			Meta: &session.EventMeta{Action: "deny", CheckPath: "static→triage→supervisor", Reason: "confirmed malicious"}},
		{SeqID: 22, Type: session.EventCheckpoint, Timestamp: t(21), Goal: "build-feature",
			Meta: &session.EventMeta{CheckpointType: "post"}},
		{SeqID: 23, Type: session.EventBashSecurity, Timestamp: t(22), Goal: "build-feature", DurationMs: 3,
			Meta: &session.EventMeta{CheckName: "deterministic", Pass: true, Source: "ls -la"}},
		{SeqID: 24, Type: session.EventBashSecurity, Timestamp: t(23), Goal: "build-feature", DurationMs: 250,
			Meta: &session.EventMeta{CheckName: "llm", Pass: false, Source: "rm -rf /", Reason: "destructive command", Model: "claude-x",
				TokensIn: 20, TokensOut: 10}},
		{SeqID: 25, Type: "unknown_event_type", Timestamp: t(24), Goal: "build-feature"},
		{SeqID: 26, Type: session.EventGoalEnd, Timestamp: t(25), Goal: "build-feature", DurationMs: 25000, Content: "feature built"},
		{SeqID: 27, Type: session.EventWorkflowEnd, Timestamp: t(26), DurationMs: 26000},
	}

	sess := &session.Session{
		ID:           "sess-golden-0001",
		WorkflowName: "build-feature-workflow",
		Inputs:       map[string]string{"repo": "agent"},
		Status:       session.StatusComplete,
		CreatedAt:    t(0),
		UpdatedAt:    t(26),
	}
	// AppendEvents, not a struct literal: events are lock-guarded, and these
	// already carry their own SeqIDs and timestamps.
	sess.AppendEvents(events...)
	return sess
}

func replayGolden(t *testing.T, name string, verbosity int, opts ...ReplayerOption) {
	t.Helper()
	sess := goldenSession()
	r := New(verbosity, opts...)
	var buf bytes.Buffer
	if err := r.Replay(&buf, sess); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got := buf.String(); got != string(want) {
		t.Errorf("Replay() at verbosity %d mismatch (-want +got):\n--- want ---\n%s\n--- got ---\n%s", verbosity, want, got)
	}
}

func TestReplay_Verbosity0(t *testing.T) { replayGolden(t, "verbosity0", 0) }
func TestReplay_Verbosity1(t *testing.T) { replayGolden(t, "verbosity1", 1) }
func TestReplay_Verbosity2(t *testing.T) { replayGolden(t, "verbosity2", 2) }

func TestReplay_WithPricing(t *testing.T) {
	replayGolden(t, "verbosity0_priced", 0, Pricing("claude-x", 3, 15))
}

func TestReplay_FailedSession(t *testing.T) {
	sess := goldenSession()
	sess.Status = session.StatusFailed
	sess.Error = "workflow aborted: security denial"

	r := New(0)
	var buf bytes.Buffer
	if err := r.Replay(&buf, sess); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	path := filepath.Join("testdata", "failed.golden")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got := buf.String(); got != string(want) {
		t.Errorf("Replay() failed-session mismatch (-want +got):\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestReplay_RunningSessionNoInputs(t *testing.T) {
	sess := &session.Session{
		ID:        "sess-empty",
		Status:    session.StatusRunning,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	r := New(0)
	var buf bytes.Buffer
	if err := r.Replay(&buf, sess); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	path := filepath.Join("testdata", "empty_running.golden")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got := buf.String(); got != string(want) {
		t.Errorf("Replay() empty-running mismatch (-want +got):\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestReplayer_ReusableAcrossCalls proves a Replayer holds no writer state:
// two concurrent Replay calls into separate buffers with the same Replayer
// must not interfere with each other.
func TestReplayer_ReusableAcrossCalls(t *testing.T) {
	r := New(0)
	sess := goldenSession()

	var buf1, buf2 bytes.Buffer
	done := make(chan error, 2)
	go func() { done <- r.Replay(&buf1, sess) }()
	go func() { done <- r.Replay(&buf2, sess) }()
	if err := <-done; err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if buf1.String() != buf2.String() {
		t.Error("concurrent Replay() calls produced different output")
	}
}
