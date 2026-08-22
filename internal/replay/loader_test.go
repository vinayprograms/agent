package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayer_ReplayFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	content := `{"_type":"header","id":"sess-1","created_at":"2026-01-01T00:00:00Z"}
{"_type":"footer","status":"complete"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(0)
	var buf bytes.Buffer
	if err := r.ReplayFile(&buf, path); err != nil {
		t.Fatalf("ReplayFile() error = %v", err)
	}
	if !strings.Contains(buf.String(), "sess-1") {
		t.Errorf("ReplayFile() output missing session ID, got:\n%s", buf.String())
	}
}

func TestReplayer_ReplayFile_MissingFile(t *testing.T) {
	r := New(0)
	var buf bytes.Buffer
	if err := r.ReplayFile(&buf, filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("ReplayFile() error = nil, want error for missing file")
	}
}

func TestReplayer_loadSession(t *testing.T) {
	dir := t.TempDir()

	jsonlPath := filepath.Join(dir, "sess.jsonl")
	jsonlContent := `{"_type":"header","id":"sess-jsonl","workflow_name":"wf","created_at":"2026-01-01T00:00:00Z"}
{"_type":"event","seq":1,"type":"user","timestamp":"2026-01-01T00:00:01Z","content":"hello"}
{"_type":"footer","status":"complete"}
`
	if err := os.WriteFile(jsonlPath, []byte(jsonlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(dir, "sess.json")
	legacyContent := `{"id":"sess-legacy","workflow_name":"wf","status":"complete","events":[{"seq":1,"type":"user","content":"hi"}]}`
	if err := os.WriteFile(legacyPath, []byte(legacyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	malformedPath := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(malformedPath, []byte(`{"_type":"header", not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	missingPath := filepath.Join(dir, "missing.jsonl")

	tests := []struct {
		name    string
		path    string
		wantErr bool
		wantID  string
	}{
		{"jsonl format", jsonlPath, false, "sess-jsonl"},
		{"legacy json format", legacyPath, false, "sess-legacy"},
		{"malformed jsonl", malformedPath, true, ""},
		{"missing file", missingPath, true, ""},
	}

	r := New(0)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, err := r.loadSession(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loadSession(%q) error = nil, want error", tt.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadSession(%q) error = %v", tt.path, err)
			}
			if sess.ID != tt.wantID {
				t.Errorf("loadSession(%q).ID = %q, want %q", tt.path, sess.ID, tt.wantID)
			}
		})
	}
}

func TestReplayer_loadSession_Truncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")
	bigContent := strings.Repeat("x", 100)
	content := `{"_type":"header","id":"sess-big","created_at":"2026-01-01T00:00:00Z"}
{"_type":"event","seq":1,"type":"user","timestamp":"2026-01-01T00:00:01Z","content":"` + bigContent + `"}
{"_type":"footer","status":"complete"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New(0, MaxContentSize(10))
	sess, err := r.loadSession(path)
	if err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	got := sess.Events[0].Content
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Errorf("Content = %q, want it truncated to 10 leading bytes", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("Content = %q, want a truncation marker", got)
	}
}
