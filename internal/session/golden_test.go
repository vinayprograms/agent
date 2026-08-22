package session

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// TestRecorder_WireFormatGolden pins the JSONL wire format byte-for-byte
// (modulo footer updated_at, which is the save time) against a fixture
// captured from the pre-refactor FileStore: header/event/footer records,
// every Event/EventMeta tag, append-only writes, and two footers.
func TestRecorder_WireFormatGolden(t *testing.T) {
	dir := t.TempDir()
	rec, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := goldenSession()
	if err := rec.Update(sess); err != nil {
		t.Fatal(err)
	}
	sess.AddEvent(Event{Type: EventWarning, Content: "late warning", Timestamp: goldenTime(99)})
	sess.Status = StatusComplete
	sess.Result = "done"
	sess.Outputs = map[string]string{"out": "final"}
	if err := rec.Update(sess); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := regexp.MustCompile(`"updated_at":"[^"]+"`)
	norm := func(b []byte) string { return string(updatedAt.ReplaceAll(b, []byte(`"updated_at":"T"`))) }
	if diff := cmp.Diff(norm(want), norm(got)); diff != "" {
		t.Errorf("wire format mismatch (-want +got):\n%s", diff)
	}

	// Round trip: the last footer wins and every event survives.
	loaded, err := rec.Get("golden")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusComplete || loaded.Result != "done" || loaded.Outputs["out"] != "final" {
		t.Errorf("last footer not applied: status=%q result=%q outputs=%v", loaded.Status, loaded.Result, loaded.Outputs)
	}
	// Event.Error is not on the wire: jsonlRecord's footer-level Error field
	// shadows the embedded Event.Error when marshaling. Pre-existing format
	// behavior, pinned by the fixture above; fixing it is a format change.
	if diff := cmp.Diff(sess.Events, loaded.Events, cmpopts.IgnoreFields(Event{}, "Error")); diff != "" {
		t.Errorf("events round trip (-want +got):\n%s", diff)
	}
}

func goldenTime(n int) time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, n*1000, time.UTC)
}

// goldenSession holds one event of every type with every field populated.
func goldenSession() *Session {
	ok, bad := true, false
	sess := &Session{
		ID:           "golden",
		WorkflowName: "golden-workflow",
		Inputs:       map[string]string{"task": "do it"},
		State:        map[string]any{"k": "v", "n": 1.5},
		Outputs:      map[string]string{},
		Status:       StatusRunning,
		Events:       []Event{},
		CreatedAt:    goldenTime(0),
		UpdatedAt:    goldenTime(0),
	}
	types := []string{
		EventSystem, EventUser, EventAssistant, EventToolCall, EventToolResult,
		EventGoalStart, EventGoalEnd, EventWorkflowStart, EventWorkflowEnd,
		EventPhaseCommit, EventPhaseExecute, EventPhaseReconcile, EventPhaseSupervise, EventCheckpoint,
		EventSecurityBlock, EventSecurityStatic, EventSecurityTriage, EventSecuritySupervisor,
		EventSecurityDecision, EventBashSecurity, EventSubAgentStart, EventSubAgentEnd, EventWarning,
	}
	for i, typ := range types {
		ev := Event{
			Type:          typ,
			Timestamp:     goldenTime(i + 1),
			CorrelationID: "c0ffee",
			ParentSeqID:   uint64(i),
			Agent:         "agent-x",
			AgentRole:     "worker",
			Goal:          "g1",
			Step:          "s1",
			Content:       "content " + typ,
			Tool:          "bash",
			Args:          map[string]any{"cmd": "ls", "n": 2.0},
			DurationMs:    int64(i * 10),
		}
		if i%2 == 0 {
			ev.Success = &ok
		} else {
			ev.Success = &bad
			ev.Error = "boom"
		}
		ev.Meta = &EventMeta{
			Phase: "COMMIT", Result: "r", Commitment: "{}", Confidence: "high", Triggers: []string{"t1"},
			Escalate: true, Verdict: "CONTINUE", Correction: "c", Guidance: "g", Human: true, HumanRequired: true,
			SupervisorType: "security", BlockID: "b0001", RelatedBlocks: []string{"b0001"},
			TaintLineage: []TaintNode{{BlockID: "b0001", Trust: "untrusted", Source: "web", EventSeq: 1, Depth: 1,
				TaintedBy: []TaintNode{{BlockID: "b0000", Trust: "trusted", Source: "user"}}}},
			Trust: "untrusted", BlockType: "data", Source: "web", Entropy: 4.5, CheckName: "static", Pass: true,
			Flags: []string{"f"}, Suspicious: true, Action: "allow", Reason: "why", CheckPath: "static→triage",
			SkipReason: "low_risk_tool", XMLBlock: "<x/>", Tier: 1, Tiers: "1", TierPath: "1",
			CheckpointType: "pre", CheckpointID: "ck1", SubAgentName: "sub", SubAgentRole: "r", SubAgentModel: "m",
			SubAgentTask: "t", SubAgentOutput: "o", SubAgentInputs: map[string]string{"a": "b"},
			Model: "model", LatencyMs: 5, TokensIn: 10, TokensOut: 20, Prompt: "p", Response: "resp", Thinking: "th",
		}
		sess.AddEvent(ev)
	}
	return sess
}
