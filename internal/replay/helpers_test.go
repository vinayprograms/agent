package replay

import (
	"testing"

	"github.com/vinayprograms/agent/internal/session"
)

func TestGetSecurityContext(t *testing.T) {
	tests := []struct {
		name  string
		event *session.Event
		want  bool // whether context is non-empty
	}{
		{"blockID and tool", &session.Event{Tool: "bash", Meta: &session.EventMeta{BlockID: "b0001"}}, true},
		{"source fallback", &session.Event{Meta: &session.EventMeta{Source: "web"}}, true},
		{"correlation ID fallback", &session.Event{CorrelationID: "11111111-2222-3333-4444-555555555555"}, true},
		{"nothing set", &session.Event{}, false},
		{"long context truncated", &session.Event{Tool: "a-very-long-tool-name-that-exceeds-the-thirty-five-char-limit"}, true},
	}
	r := New(0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.getSecurityContext(tt.event)
			if (got != "") != tt.want {
				t.Errorf("getSecurityContext() = %q, want non-empty=%v", got, tt.want)
			}
		})
	}
}

func TestGetAgentPrefix(t *testing.T) {
	r := New(0)
	tests := []struct {
		name  string
		event *session.Event
		want  string
	}{
		{"no agent", &session.Event{}, ""},
		{"main role hidden", &session.Event{AgentRole: "main"}, ""},
		{"role shown", &session.Event{AgentRole: "reviewer"}, "reviewer"},
		{"agent name fallback", &session.Event{Agent: "worker"}, "worker"},
		{"long name truncated", &session.Event{AgentRole: "a-role-name-longer-than-twenty-chars"}, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.getAgentPrefix(tt.event)
			if tt.want == "" {
				if got != "" {
					t.Errorf("getAgentPrefix() = %q, want empty", got)
				}
				return
			}
			if got == "" {
				t.Errorf("getAgentPrefix() = empty, want it to contain %q", tt.want)
			}
		})
	}
}

func TestGetArgsHint(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want bool
	}{
		{"nil args", "web_search", nil, false},
		{"web_search", "web_search", map[string]any{"query": "golang testing"}, true},
		{"web_fetch", "web_fetch", map[string]any{"url": "http://example.com"}, true},
		{"read path", "read", map[string]any{"path": "/tmp/x"}, true},
		{"bash command", "bash", map[string]any{"command": "ls"}, true},
		{"spawn_agent task", "spawn_agent", map[string]any{"task": "do it"}, true},
		{"unknown tool", "mystery", map[string]any{"x": "y"}, false},
		{"wrong-typed arg", "bash", map[string]any{"command": 5}, false},
	}
	r := New(0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.getArgsHint(tt.tool, tt.args)
			if (got != "") != tt.want {
				t.Errorf("getArgsHint(%q, %v) = %q, want non-empty=%v", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestTruncateHint(t *testing.T) {
	tests := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"a very long string that needs truncating", 10, "a very ..."},
		{"line1\nline2", 20, "line1 line2"},
	}
	for _, tt := range tests {
		if got := truncateHint(tt.in, tt.maxLen); got != tt.want {
			t.Errorf("truncateHint(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
		}
	}
}

func TestTruncateContent(t *testing.T) {
	tests := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"this is too long", 7, "this is..."},
	}
	for _, tt := range tests {
		if got := truncateContent(tt.in, tt.maxLen); got != tt.want {
			t.Errorf("truncateContent(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
		}
	}
}

func TestFormatMap(t *testing.T) {
	if got := formatMap(map[string]string{"a": "1"}); got != "a=1" {
		t.Errorf("formatMap() = %q, want %q", got, "a=1")
	}
	if got := formatMap(nil); got != "" {
		t.Errorf("formatMap(nil) = %q, want empty", got)
	}
}

func TestVerdictStyle_AllBranches(t *testing.T) {
	r := New(0)
	for _, v := range []string{"CONTINUE", "REORIENT", "PAUSE", "unknown"} {
		if s := r.verdictStyle(v); s.Render(v) == "" {
			t.Errorf("verdictStyle(%q) rendered empty", v)
		}
	}
}

func TestActionStyle_AllBranches(t *testing.T) {
	r := New(0)
	for _, a := range []string{"allow", "deny", "modify", "unknown"} {
		if s := r.actionStyle(a); s.Render(a) == "" {
			t.Errorf("actionStyle(%q) rendered empty", a)
		}
	}
}

func TestStatusStyle_AllBranches(t *testing.T) {
	r := New(0)
	for _, s := range []string{session.StatusComplete, session.StatusFailed, session.StatusRunning, "unknown"} {
		if st := r.statusStyle(s); st.Render(s) == "" {
			t.Errorf("statusStyle(%q) rendered empty", s)
		}
	}
}
