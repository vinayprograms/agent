package replay

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/session"
)

// TestFormatEvent_NilMetaGuards exercises the defensive nil-Meta early
// returns in fmtSubAgentStart, fmtSubAgentEnd, fmtSecurityBlock and
// fmtSecurityDecision — none of these should panic or write anything.
func TestFormatEvent_NilMetaGuards(t *testing.T) {
	r := New(0)
	events := []session.Event{
		{Type: session.EventSubAgentStart},
		{Type: session.EventSubAgentEnd},
		{Type: session.EventSecurityBlock},
		{Type: session.EventSecurityDecision},
		{Type: session.EventCheckpoint}, // fmtCheckpoint also guards on nil Meta
	}
	var lastGoal string
	for i, e := range events {
		var buf bytes.Buffer
		r.formatEvent(&buf, i+1, &e, &lastGoal)
		if buf.Len() != 0 {
			t.Errorf("formatEvent(%s) with nil Meta wrote %q, want nothing", e.Type, buf.String())
		}
	}
}

func TestFmtSubAgentStart_NoModel(t *testing.T) {
	r := New(0)
	e := session.Event{Type: session.EventSubAgentStart, Meta: &session.EventMeta{SubAgentName: "worker"}}
	var buf bytes.Buffer
	var lastGoal string
	r.formatEvent(&buf, 1, &e, &lastGoal)
	if !strings.Contains(buf.String(), "worker") {
		t.Errorf("output missing subagent name, got: %s", buf.String())
	}
}

func TestFmtSubAgentEnd_Error(t *testing.T) {
	r := New(0)
	e := session.Event{Type: session.EventSubAgentEnd, Error: "boom",
		Meta: &session.EventMeta{SubAgentName: "worker"}}
	var buf bytes.Buffer
	var lastGoal string
	r.formatEvent(&buf, 1, &e, &lastGoal)
	out := buf.String()
	if !strings.Contains(out, "failed") || !strings.Contains(out, "boom") {
		t.Errorf("output missing failed status/error, got: %s", out)
	}
}

func TestFmtSecurityBlock_ToolFallbackAndNoSource(t *testing.T) {
	r := New(0)

	// Source empty, Tool set -> falls back to Tool.
	e := session.Event{Type: session.EventSecurityBlock, Tool: "web_fetch",
		Meta: &session.EventMeta{BlockID: "b0001"}}
	var buf bytes.Buffer
	var lastGoal string
	r.formatEvent(&buf, 1, &e, &lastGoal)
	if !strings.Contains(buf.String(), "web_fetch") {
		t.Errorf("output missing tool fallback source, got: %s", buf.String())
	}

	// Neither Source nor Tool set, no RelatedBlocks -> no arrow, no "tainted by" line.
	e2 := session.Event{Type: session.EventSecurityBlock,
		Meta: &session.EventMeta{BlockID: "b0002"}}
	var buf2 bytes.Buffer
	r.formatEvent(&buf2, 2, &e2, &lastGoal)
	out2 := buf2.String()
	if strings.Contains(out2, "←") {
		t.Errorf("output should have no source arrow, got: %s", out2)
	}
	if strings.Contains(out2, "tainted by") {
		t.Errorf("output should have no tainted-by line, got: %s", out2)
	}
}

func TestPrintSubAgentOutput_Truncation(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")

	r := New(0) // maxLines = 10 at verbosity 0
	var buf bytes.Buffer
	r.printSubAgentOutput(&buf, content)
	if !strings.Contains(buf.String(), "more lines") {
		t.Errorf("expected truncation marker, got:\n%s", buf.String())
	}

	rVerbose := New(1) // maxLines = 50, still truncates 60 lines
	var buf2 bytes.Buffer
	rVerbose.printSubAgentOutput(&buf2, content)
	if !strings.Contains(buf2.String(), "more lines") {
		t.Errorf("expected truncation marker at verbosity 1, got:\n%s", buf2.String())
	}
}

func TestPrintTaintLineage_Empty(t *testing.T) {
	r := New(0)
	var buf bytes.Buffer
	r.printTaintLineage(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("printTaintLineage(nil) wrote %q, want nothing", buf.String())
	}
}

func TestFmtPhaseCommit_LongCommitmentVerbosity2(t *testing.T) {
	r := New(2)
	e := session.Event{
		Type: session.EventPhaseCommit,
		Meta: &session.EventMeta{Commitment: strings.Repeat("x", 100)},
	}
	var buf bytes.Buffer
	var lastGoal string
	r.formatEvent(&buf, 1, &e, &lastGoal)
	if !strings.Contains(buf.String(), "COMMITMENT") {
		t.Errorf("expected full commitment block at verbosity 2, got:\n%s", buf.String())
	}
}

// TestFmtToolResult_ErrorFromMeta covers the replay path for a persisted
// session: Event.Error does not survive the JSONL round trip (jsonlRecord's
// footer-level "error" field shadows it), so a failed tool_result's error
// text lives in meta.error instead. Replay must still render it as a
// failure with the error text shown.
func TestFmtToolResult_ErrorFromMeta(t *testing.T) {
	r := New(0)
	e := session.Event{
		Type: session.EventToolResult,
		Tool: "bash",
		Meta: &session.EventMeta{Error: "exit status 1"},
	}
	var buf bytes.Buffer
	var lastGoal string
	r.formatEvent(&buf, 1, &e, &lastGoal)
	out := buf.String()
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "exit status 1") {
		t.Errorf("output = %q, want it to show the tool failed with the meta.error text", out)
	}
}
