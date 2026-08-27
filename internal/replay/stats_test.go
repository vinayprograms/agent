package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/session"
)

func TestComputeStats(t *testing.T) {
	sess := goldenSession()
	stats := ComputeStats(sess)

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"TotalDurationMs", stats.TotalDurationMs, int64(26000)},
		{"GoalDurations[build-feature]", stats.GoalDurations["build-feature"], int64(25000)},
		{"len(GoalDurations)", len(stats.GoalDurations), 1},

		{"LLMCallCount", stats.LLMCallCount, 1},
		{"LLMTotalMs", stats.LLMTotalMs, int64(1200)},
		{"LLMAvgMs", stats.LLMAvgMs, int64(1200)},

		{"ExecSupervisorCount", stats.ExecSupervisorCount, 2},
		{"ExecSupervisorTotalMs", stats.ExecSupervisorTotalMs, int64(600)},
		{"ExecSupervisorAvgMs", stats.ExecSupervisorAvgMs, int64(300)},

		{"SecurityTriageCount", stats.SecurityTriageCount, 0},
		{"SecurityTriageTotalMs", stats.SecurityTriageTotalMs, int64(0)},
		{"SecurityTriageAvgMs", stats.SecurityTriageAvgMs, int64(0)},

		{"SecuritySupervisorCount", stats.SecuritySupervisorCount, 0},
		{"SecuritySupervisorTotalMs", stats.SecuritySupervisorTotalMs, int64(0)},
		{"SecuritySupervisorAvgMs", stats.SecuritySupervisorAvgMs, int64(0)},

		{"BashDeterministicCount", stats.BashDeterministicCount, 1},
		{"BashLLMCount", stats.BashLLMCount, 1},
		{"BashLLMTotalMs", stats.BashLLMTotalMs, int64(250)},
		{"BashLLMAvgMs", stats.BashLLMAvgMs, int64(250)},

		{"len(ModelUsage)", len(stats.ModelUsage), 1},
		{"ModelUsage[claude-x].Calls", stats.ModelUsage["claude-x"].Calls, 5},
		{"ModelUsage[claude-x].TokensIn", stats.ModelUsage["claude-x"].TokensIn, int64(120)},
		{"ModelUsage[claude-x].TokensOut", stats.ModelUsage["claude-x"].TokensOut, int64(60)},
		{"ModelUsage[claude-y] absent", stats.ModelUsage["claude-y"] == nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("ComputeStats().%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestComputeStats_Empty(t *testing.T) {
	stats := ComputeStats(&session.Session{})
	if stats.TotalDurationMs != 0 {
		t.Errorf("TotalDurationMs = %d, want 0", stats.TotalDurationMs)
	}
	if len(stats.ModelUsage) != 0 {
		t.Errorf("len(ModelUsage) = %d, want 0", len(stats.ModelUsage))
	}
}

// TestComputeStats_BashSecurityFallback exercises the legacy path that
// parses "[deterministic] ..." / "[llm] ..." out of Content when Meta is
// nil (older sessions predating structured bash-security Meta).
func TestComputeStats_BashSecurityFallback(t *testing.T) {
	sess := &session.Session{}
	sess.AppendEvents([]session.Event{
		{Type: session.EventBashSecurity, Content: "[deterministic] allow: ls"},
		{Type: session.EventBashSecurity, Content: "[llm] deny: rm -rf /", DurationMs: 12},
	}...)
	stats := ComputeStats(sess)
	if stats.BashDeterministicCount != 1 {
		t.Errorf("BashDeterministicCount = %d, want 1", stats.BashDeterministicCount)
	}
	if stats.BashLLMCount != 1 {
		t.Errorf("BashLLMCount = %d, want 1", stats.BashLLMCount)
	}
	if stats.BashLLMTotalMs != 12 {
		t.Errorf("BashLLMTotalMs = %d, want 12", stats.BashLLMTotalMs)
	}
}

func TestPrintStats_Golden(t *testing.T) {
	sess := goldenSession()
	stats := ComputeStats(sess)

	var buf bytes.Buffer
	PrintStats(&buf, stats)
	PrintTokenUsage(&buf, stats, PricingMap{
		"claude-x": {InputPer1M: 3, OutputPer1M: 15},
	})

	path := filepath.Join("testdata", "stats.golden")
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
		t.Errorf("PrintStats/PrintTokenUsage mismatch (-want +got):\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestComputeStats_SecurityLatencyFallback(t *testing.T) {
	sess := &session.Session{}
	sess.AppendEvents([]session.Event{
		// DurationMs takes priority over Meta.LatencyMs.
		{Type: session.EventSecurityTriage, DurationMs: 100, Meta: &session.EventMeta{LatencyMs: 999}},
		// Falls back to Meta.LatencyMs when DurationMs is 0.
		{Type: session.EventSecuritySupervisor, Meta: &session.EventMeta{LatencyMs: 50}},
	}...)
	stats := ComputeStats(sess)
	if stats.SecurityTriageTotalMs != 100 {
		t.Errorf("SecurityTriageTotalMs = %d, want 100 (DurationMs wins over Meta.LatencyMs)", stats.SecurityTriageTotalMs)
	}
	if stats.SecuritySupervisorTotalMs != 50 {
		t.Errorf("SecuritySupervisorTotalMs = %d, want 50 (Meta.LatencyMs fallback)", stats.SecuritySupervisorTotalMs)
	}
}

func TestAddModelUsage_EmptyModelSkipped(t *testing.T) {
	s := &Stats{ModelUsage: make(map[string]*ModelUsage)}
	s.addModelUsage("", 10, 10)
	if len(s.ModelUsage) != 0 {
		t.Errorf("addModelUsage(\"\", ...) added an entry, want it skipped")
	}
}

func TestPrintStats_NoGoalsNoLLM(t *testing.T) {
	var buf bytes.Buffer
	PrintStats(&buf, &Stats{})
	out := buf.String()
	if strings.Contains(out, "Goal Durations:") {
		t.Error("PrintStats() printed a Goal Durations section with no goals")
	}
	if strings.Contains(out, "LLM Response Times:") {
		t.Error("PrintStats() printed an LLM section with no LLM calls")
	}
}

func TestPrintTokenUsage_NoModelUsage(t *testing.T) {
	var buf bytes.Buffer
	PrintTokenUsage(&buf, &Stats{}, nil)
	if buf.Len() != 0 {
		t.Errorf("PrintTokenUsage() with no model usage wrote %d bytes, want 0", buf.Len())
	}
}

// TestPrintStats_SecurityAndBashSections exercises the "Security Checks"
// and "Bash Security" sections of PrintStats, which the golden fixture
// leaves unhit because its triage/supervisor events carry neither
// DurationMs nor Meta.LatencyMs.
func TestPrintStats_SecurityAndBashSections(t *testing.T) {
	stats := &Stats{
		SecurityTriageCount:     2,
		SecurityTriageAvgMs:     10,
		SecuritySupervisorCount: 1,
		SecuritySupervisorAvgMs: 20,
		BashDeterministicCount:  3,
		BashLLMCount:            1,
		BashLLMAvgMs:            15,
	}
	var buf bytes.Buffer
	PrintStats(&buf, stats)
	out := buf.String()
	for _, want := range []string{"Security Checks:", "Triage (Tier 2):", "Supervisor (Tier 3):", "Bash Security:", "Deterministic:", "LLM:"} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintStats() output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintTokenUsage_MissingPricing exercises the "(no pricing)" branch
// for a model that has usage but no entry in the pricing map.
func TestPrintTokenUsage_MissingPricing(t *testing.T) {
	stats := &Stats{
		ModelUsage: map[string]*ModelUsage{
			"unpriced-model": {Calls: 1, TokensIn: 10, TokensOut: 5},
		},
	}
	var buf bytes.Buffer
	PrintTokenUsage(&buf, stats, PricingMap{"other-model": {InputPer1M: 1, OutputPer1M: 1}})
	if !strings.Contains(buf.String(), "(no pricing)") {
		t.Errorf("PrintTokenUsage() missing (no pricing) marker:\n%s", buf.String())
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{999, "999ms"},
		{1000, "1.00s"},
		{59949, "59.95s"},
		{60000, "1m0s"},
		{125000, "2m5s"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.ms); got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		tokens int64
		want   string
	}{
		{0, "0 tokens"},
		{999, "999 tokens"},
		{1000, "1,000 tokens"},
		{1234567, "1,234,567 tokens"},
	}
	for _, tt := range tests {
		if got := formatTokens(tt.tokens); got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.tokens, got, tt.want)
		}
	}
}
