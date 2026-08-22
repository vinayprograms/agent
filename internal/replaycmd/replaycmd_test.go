package replaycmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sessionContent = `{"_type":"header","id":"sess-1","created_at":"2026-01-01T00:00:00Z"}
{"_type":"footer","status":"complete"}
`

func writeSession(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	// Isolate the state directory: nothing here may read the real one.
	t.Setenv("HOME", t.TempDir())
	cmd := New(Config{Use: "agent-replay", Version: "1.2.3", Commit: "abc", BuildTime: "today"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return buf.String(), err
}

func TestReplay_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "sess.jsonl")
	out, err := runCmd(t, "--no-pager", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sess-1") {
		t.Errorf("output missing session id: %s", out)
	}
}

func TestReplay_NoSessionsRecorded(t *testing.T) {
	out, err := runCmd(t)
	if err == nil {
		t.Fatalf("expected a friendly error for an empty state directory, got: %s", out)
	}
	if !strings.Contains(err.Error(), "no sessions recorded yet") || !strings.Contains(err.Error(), "sessions") {
		t.Errorf("error = %v, want it to name the missing sessions directory", err)
	}
}

func TestReplay_Version(t *testing.T) {
	out, err := runCmd(t, "--version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "1.2.3") || !strings.Contains(out, "abc") || !strings.Contains(out, "today") {
		t.Errorf("version output missing fields: %s", out)
	}
}

func TestReplay_VersionDefaults(t *testing.T) {
	cmd := New(Config{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "dev") || !strings.Contains(buf.String(), "unknown") {
		t.Errorf("expected default version fields, got: %s", buf.String())
	}
}

func TestReplay_DirectoryGlobsJSONLAndJSON(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "a.jsonl")
	legacy := `{"id":"sess-1","workflow_name":"w","status":"complete","events":[]}`
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSession(t, dir, "c.txt") // ignored: not .jsonl or .json

	out, err := runCmd(t, "--no-pager", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(out, "sess-1") < 2 {
		t.Errorf("expected both .jsonl and .json sessions replayed, got: %s", out)
	}
}

func TestReplay_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	p1 := writeSession(t, dir, "one.jsonl")
	p2 := writeSession(t, dir, "two.jsonl")
	out, err := runCmd(t, "--no-pager", p1, p2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Count(out, "sess-1") < 2 {
		t.Errorf("expected two sessions replayed, got: %s", out)
	}
}

func TestReplay_CostFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "sess.jsonl")
	if _, err := runCmd(t, "--no-pager", "--cost", "gpt:1,2", path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplay_CostFlag_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "sess.jsonl")
	if _, err := runCmd(t, "--no-pager", "--cost", "bad", path); err == nil {
		t.Error("expected error for invalid cost spec")
	}
}

func TestReplay_VerboseFlags(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "sess.jsonl")
	if _, err := runCmd(t, "--no-pager", "-vv", path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := runCmd(t, "--no-pager", "-v", path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplay_NoSessionFilesFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCmd(t, "--no-pager", dir); err == nil {
		t.Error("expected error when directory has no session files")
	}
}

func TestReplay_MissingPath(t *testing.T) {
	if _, err := runCmd(t, "--no-pager", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing path")
	}
}

func TestReplay_Follow_RequiresSingleFile(t *testing.T) {
	dir := t.TempDir()
	p1 := writeSession(t, dir, "one.jsonl")
	p2 := writeSession(t, dir, "two.jsonl")
	if _, err := runCmd(t, "-f", p1, p2); err == nil {
		t.Error("expected error when --follow given multiple files")
	}
}

func TestReplay_Follow_RequiresFileNotDir(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "a.jsonl")
	if _, err := runCmd(t, "-f", dir); err == nil {
		t.Error("expected error when --follow given a directory")
	}
}

func TestReplay_Follow_MissingFile(t *testing.T) {
	if _, err := runCmd(t, "-f", filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("expected error for missing follow target")
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("expected temp file to not be a terminal")
	}
	if isTerminal(&bytes.Buffer{}) {
		t.Error("expected non-*os.File writer to not be a terminal")
	}
	f.Close()
	if isTerminal(f) {
		t.Error("expected a closed file's Stat error to report not-a-terminal")
	}
}

func TestReplay_DirectoryGlobError(t *testing.T) {
	parent := t.TempDir()
	bad := filepath.Join(parent, "bad[dir")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "--no-pager", bad); err == nil {
		t.Error("expected glob error for a directory with an unmatched '[' in its path")
	}
}

func TestReplay_LiveAlias(t *testing.T) {
	// --live is an alias for --follow; a missing file should hit the same
	// stat error path as -f/--follow.
	if _, err := runCmd(t, "--live", filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("expected error for missing --live target")
	}
}
