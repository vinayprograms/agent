package replay

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// TestMultiReplayer_Ordering proves ReplayFiles sorts sessions by
// CreatedAt regardless of the order paths (and thus loaded sessions) are
// given in.
func TestMultiReplayer_Ordering(t *testing.T) {
	dir := t.TempDir()

	older := writeSession(t, dir, "older", "older-agent", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := writeSession(t, dir, "newer", "newer-agent", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	m := NewMulti(0)
	var buf bytes.Buffer
	// Pass newer before older: output must still be older-first.
	if err := m.ReplayFiles(&buf, []string{newer, older}); err != nil {
		t.Fatalf("ReplayFiles() error = %v", err)
	}

	out := buf.String()
	idxOlder := strings.Index(out, "older-agent")
	idxNewer := strings.Index(out, "newer-agent")
	if idxOlder == -1 || idxNewer == -1 {
		t.Fatalf("expected both agent names in output, got:\n%s", out)
	}
	if idxOlder > idxNewer {
		t.Errorf("older session (idx %d) rendered after newer session (idx %d); want chronological order", idxOlder, idxNewer)
	}
}

func TestInferAgentName(t *testing.T) {
	tests := []struct {
		name string
		sess *session.Session
		path string
		want string
	}{
		{"workflow name wins", &session.Session{WorkflowName: "wf"}, "/x/sess.jsonl", "wf"},
		{"filename fallback", &session.Session{}, "/x/my-session.jsonl", "my-session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferAgentName(tt.sess, tt.path); got != tt.want {
				t.Errorf("inferAgentName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMultiReplayer_LoadError(t *testing.T) {
	m := NewMulti(0)
	var buf bytes.Buffer
	err := m.ReplayFiles(&buf, []string{"/nonexistent/session.jsonl"})
	if err == nil {
		t.Fatal("ReplayFiles() error = nil, want error for missing file")
	}
}

// writeSession writes a minimal JSONL session file (header + footer, no
// events) and returns its path. Written directly, rather than through
// [session.Recorder], so CreatedAt can be controlled for ordering tests.
func writeSession(t *testing.T, dir, id, workflowName string, createdAt time.Time) string {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	header := fmt.Sprintf(`{"_type":"header","id":%q,"workflow_name":%q,"created_at":%q}`,
		id, workflowName, createdAt.Format(time.RFC3339))
	footer := `{"_type":"footer","status":"complete"}`
	content := header + "\n" + footer + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	return path
}
