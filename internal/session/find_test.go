package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeJSONL writes a minimal session file at root/rel.
func writeJSONL(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	// Flat: the current layout, with the deployment fields.
	flat := writeJSONL(t, root, "aaa.jsonl",
		`{"_type":"header","id":"aaa","workflow_name":"hello","agentfile":"/w/Agentfile","label":"worker-1","created_at":"2026-01-02T00:00:00Z"}
{"_type":"event","seq":1,"type":"user","content":"hi"}
{"_type":"footer","status":"running","updated_at":"2026-01-02T00:00:01Z"}
{"_type":"footer","status":"complete","updated_at":"2026-01-02T00:01:00Z"}
`)
	// Nested: an old layout, still readable.
	nested := writeJSONL(t, root, filepath.Join("hello", "bbb.jsonl"),
		`{"_type":"header","id":"bbb","workflow_name":"hello","created_at":"2026-01-01T00:00:00Z"}
{"_type":"footer","status":"failed","error":"boom","updated_at":"2026-01-01T00:00:30Z"}
`)
	// Ignored: not a session file.
	writeJSONL(t, root, "notes.txt", "not a session\n")

	got, err := Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []Summary{
		{ID: "bbb", Path: nested, Name: "hello", Status: StatusFailed, Error: "boom",
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)},
		{ID: "aaa", Path: flat, Name: "hello", Agentfile: "/w/Agentfile", Label: "worker-1", Status: StatusComplete,
			CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 2, 0, 1, 0, 0, time.UTC)},
	}
	if len(got) != len(want) {
		t.Fatalf("Find returned %d summaries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].CreatedAt.Equal(want[i].CreatedAt) || !got[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			t.Errorf("summary %d times = %v/%v, want %v/%v", i, got[i].CreatedAt, got[i].UpdatedAt, want[i].CreatedAt, want[i].UpdatedAt)
		}
		got[i].CreatedAt, got[i].UpdatedAt = want[i].CreatedAt, want[i].UpdatedAt
		if got[i] != want[i] {
			t.Errorf("summary %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFind_LegacyJSONAndMalformed(t *testing.T) {
	root := t.TempDir()
	writeJSONL(t, root, "legacy.json",
		`{"id":"legacy","workflow_name":"old","status":"complete","events":[],"created_at":"2026-01-03T00:00:00Z"}`)
	writeJSONL(t, root, "broken.jsonl", `{"_type":"header","id":`+"\n")
	writeJSONL(t, root, "headerless.jsonl", `{"_type":"footer","status":"complete"}`+"\n")
	writeJSONL(t, root, "badfooter.jsonl",
		`{"_type":"header","id":"bf","created_at":"2026-01-03T00:00:00Z"}`+"\n"+`{"_type":"footer","status":}`+"\n")

	got, err := Find(root)
	if err == nil {
		t.Fatal("expected an error naming the malformed files")
	}
	for _, name := range []string{"broken.jsonl", "headerless.jsonl", "badfooter.jsonl"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %v does not name %s", err, name)
		}
	}
	if len(got) != 1 || got[0].ID != "legacy" || got[0].Name != "old" || got[0].Status != StatusComplete {
		t.Errorf("summaries = %+v, want the legacy session only", got)
	}
}

func TestFind_MissingRoot(t *testing.T) {
	got, err := Find(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(got) != 0 {
		t.Errorf("Find(missing) = %v, %v; want no summaries and no error", got, err)
	}
}

func TestFind_RecordedSession(t *testing.T) {
	dir := t.TempDir()
	rec, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rec.Create(Meta{Name: "wf", Agentfile: "/a/Agentfile", Label: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	sess.AddEvent(Event{Type: EventUser, Content: "hello"})
	sess.Status = StatusComplete
	if err := rec.Update(sess); err != nil {
		t.Fatal(err)
	}
	sess.Close()

	got, err := Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Find returned %d summaries, want 1", len(got))
	}
	s := got[0]
	if s.ID != sess.ID || s.Path != filepath.Join(dir, sess.ID+".jsonl") ||
		s.Name != "wf" || s.Agentfile != "/a/Agentfile" || s.Label != "svc" || s.Status != StatusComplete {
		t.Errorf("summary = %+v", s)
	}
}

func TestFind_UnreadableFilesAreReported(t *testing.T) {
	root := t.TempDir()
	good := writeJSONL(t, root, "ok.jsonl",
		`{"_type":"header","id":"ok","created_at":"2026-01-01T00:00:00Z"}`+"\n")
	badJSON := writeJSONL(t, root, "bad.json", `{"events":[`)
	secret := writeJSONL(t, root, "secret.jsonl", `{"_type":"header","id":"s"}`+"\n")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	got, err := Find(root)
	if err == nil {
		t.Fatal("expected errors for the unreadable entries")
	}
	for _, name := range []string{filepath.Base(badJSON), filepath.Base(secret), "locked"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %v does not name %s", err, name)
		}
	}
	if len(got) != 1 || got[0].Path != good {
		t.Errorf("summaries = %+v, want the readable session only", got)
	}
}

// TestSummarizeJSONL_ReadError covers the read failure that only a
// non-file can provoke: reading a directory as a session file.
func TestSummarizeJSONL_ReadError(t *testing.T) {
	if _, err := summarizeJSONL(t.TempDir()); err == nil {
		t.Error("expected a read error for a directory")
	}
}
