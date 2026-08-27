package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

func mustOpen(t *testing.T, dir string, sink Sink) *Recorder {
	t.Helper()
	rec, err := Open(dir, sink)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", dir, err)
	}
	return rec
}

// eventsOnDisk reads back the session file and returns its event types.
func eventsOnDisk(t *testing.T, rec *Recorder, id string) []string {
	t.Helper()
	s, err := rec.Get(id)
	if err != nil {
		t.Fatalf("Get(%q) error: %v", id, err)
	}
	types := make([]string, len(s.events))
	for i, e := range s.events {
		types[i] = e.Type
	}
	return types
}

func TestOpen(t *testing.T) {
	t.Run("creates dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a", "b")
		mustOpen(t, dir, nil)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("Open did not create %s: %v", dir, err)
		}
	})
	t.Run("error", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "f")
		os.WriteFile(file, nil, 0o644)
		if _, err := Open(filepath.Join(file, "x"), nil); err == nil {
			t.Error("Open under a file: want error")
		}
	})
}

func TestRecorder_Create(t *testing.T) {
	rec := mustOpen(t, t.TempDir(), nil)
	ids := map[string]bool{}
	for range 50 {
		s, err := rec.Create(Meta{Name: "wf"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
		s.Close()
		if len(s.ID) != 32 || ids[s.ID] {
			t.Errorf("Create ID %q: want 32 hex chars, unique", s.ID)
		}
		ids[s.ID] = true
		if s.WorkflowName != "wf" || s.Status != StatusRunning || s.CreatedAt.IsZero() {
			t.Errorf("Create session = %+v", s)
		}
	}
	// Header + footer exist before any event.
	s, _ := rec.Create(Meta{Name: "wf"})
	defer s.Close()
	got, err := rec.Get(s.ID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got.ID != s.ID || got.WorkflowName != "wf" || got.Status != StatusRunning || len(got.events) != 0 {
		t.Errorf("Get after Create = %+v", got)
	}

	t.Run("error", func(t *testing.T) {
		dir := t.TempDir()
		rec := mustOpen(t, dir, nil)
		os.RemoveAll(dir)
		if _, err := rec.Create(Meta{Name: "wf"}); err == nil {
			t.Error("Create with missing dir: want error")
		}
	})
}

func TestRecorder_Update(t *testing.T) {
	t.Run("append only", func(t *testing.T) {
		rec := mustOpen(t, t.TempDir(), nil)
		s := &Session{ID: "s1", Status: StatusRunning}
		s.AddEvent(Event{Type: EventGoalStart})
		if err := rec.Update(s); err != nil {
			t.Fatal(err)
		}
		s.AddEvent(Event{Type: EventGoalEnd})
		s.Status = StatusFailed
		s.Error = "x"
		if err := rec.Update(s); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(rec.dir, "s1.jsonl"))
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 5 { // header, event, footer, event, footer
			t.Fatalf("file has %d lines, want 5:\n%s", len(lines), raw)
		}
		got, err := rec.Get("s1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusFailed || got.Error != "x" || len(got.events) != 2 || got.UpdatedAt.IsZero() {
			t.Errorf("Get = %+v", got)
		}
		if got.seq.Load() != 2 || got.written != 2 {
			t.Errorf("seq=%d written=%d, want 2, 2", got.seq.Load(), got.written)
		}
	})
	t.Run("marshal error", func(t *testing.T) {
		rec := mustOpen(t, t.TempDir(), nil)
		s := &Session{ID: "bad"}
		s.AddEvent(Event{Args: map[string]any{"ch": make(chan int)}})
		if err := rec.Update(s); err == nil || !strings.Contains(err.Error(), "marshal event") {
			t.Errorf("Update error = %v, want marshal event error", err)
		}
	})
	t.Run("open error", func(t *testing.T) {
		dir := t.TempDir()
		rec := mustOpen(t, dir, nil)
		os.MkdirAll(filepath.Join(dir, "d.jsonl"), 0o755)
		if err := rec.Update(&Session{ID: "d"}); err == nil {
			t.Error("Update on a directory path: want error")
		}
	})
	t.Run("sync error", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("fsync on /dev/null only fails on darwin")
		}
		dir := t.TempDir()
		rec := mustOpen(t, dir, nil)
		os.Symlink("/dev/null", filepath.Join(dir, "null.jsonl"))
		if err := rec.Update(&Session{ID: "null"}); err == nil || !strings.Contains(err.Error(), "write file") {
			t.Errorf("Update error = %v, want write error", err)
		}
	})
}

func TestRecorder_Get(t *testing.T) {
	rec := mustOpen(t, t.TempDir(), nil)
	legacy := `{"id":"old","workflow_name":"w","status":"complete","events":[{"seq":7,"type":"goal_end"}]}`
	os.WriteFile(filepath.Join(rec.dir, "old.json"), []byte(legacy), 0o644)
	got, err := rec.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "old" || len(got.events) != 1 || got.seq.Load() != 7 {
		t.Errorf("Get legacy = %+v", got)
	}
	if _, err := rec.Get("missing"); err == nil {
		t.Error("Get missing: want error")
	}
}

func TestSession_ZeroValue(t *testing.T) {
	var s Session
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if seq := s.AddEvent(Event{Type: EventUser, Timestamp: fixed}); seq != 1 {
		t.Errorf("first SeqID = %d, want 1", seq)
	}
	if seq := s.AddEvent(Event{Type: EventAssistant}); seq != 2 {
		t.Errorf("second SeqID = %d, want 2", seq)
	}
	s.Flush()
	s.Close()
	if len(s.events) != 2 || !s.events[0].Timestamp.Equal(fixed) || s.events[1].Timestamp.IsZero() {
		t.Errorf("events = %+v", s.events)
	}
	if s.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
	if a, b := s.StartCorrelation(), s.StartCorrelation(); len(a) != 8 || a == b {
		t.Errorf("StartCorrelation = %q, %q: want 8 hex chars, unique", a, b)
	}
}

// TestSession_ConcurrentAddEventFlushClose drives AddEvent from many
// goroutines while Flush and Close race in from others, on both a
// zero-value Session (in-memory only) and a Recorder-backed one (real
// writer). Run with -race; the point is no data race and no panic.
func TestSession_ConcurrentAddEventFlushClose(t *testing.T) {
	run := func(t *testing.T, s *Session) {
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.AddEvent(Event{Type: EventUser})
			}()
		}
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.Flush()
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Close()
		}()
		wg.Wait()
		s.Close() // Close must stay idempotent after the race.
	}

	t.Run("zero value", func(t *testing.T) {
		run(t, &Session{})
	})

	t.Run("recorder-backed", func(t *testing.T) {
		rec := mustOpen(t, t.TempDir(), nil)
		s, err := rec.Create(Meta{Name: "wf"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
		run(t, s)
	})
}

func TestWriter(t *testing.T) {
	t.Run("batch size", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var seen []uint64
			rec := mustOpen(t, t.TempDir(), func(e Event) { seen = append(seen, e.SeqID) })
			s, _ := rec.Create(Meta{Name: "wf"})
			defer s.Close()
			for range batchSizeMax - 1 {
				s.AddEvent(Event{Type: EventUser})
			}
			synctest.Wait()
			if n := len(eventsOnDisk(t, rec, s.ID)); n != 0 {
				t.Errorf("%d events on disk before batch full, want 0", n)
			}
			s.AddEvent(Event{Type: EventUser})
			synctest.Wait()
			if n := len(eventsOnDisk(t, rec, s.ID)); n != batchSizeMax {
				t.Errorf("%d events on disk after batch full, want %d", n, batchSizeMax)
			}
			if len(seen) != batchSizeMax || seen[0] != 1 || seen[batchSizeMax-1] != batchSizeMax {
				t.Errorf("sink saw %v", seen)
			}
		})
	})
	t.Run("ticker", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := mustOpen(t, t.TempDir(), nil)
			s, _ := rec.Create(Meta{Name: "wf"})
			defer s.Close()
			s.AddEvent(Event{Type: EventUser})
			time.Sleep(flushInterval - time.Millisecond)
			synctest.Wait()
			if n := len(eventsOnDisk(t, rec, s.ID)); n != 0 {
				t.Errorf("%d events on disk before tick, want 0", n)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			if n := len(eventsOnDisk(t, rec, s.ID)); n != 1 {
				t.Errorf("%d events on disk after tick, want 1", n)
			}
			time.Sleep(flushInterval) // empty tick persists nothing
			synctest.Wait()
		})
	})
	t.Run("flush and close", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := mustOpen(t, t.TempDir(), nil)
			s, _ := rec.Create(Meta{Name: "wf"})
			s.AddEvent(Event{Type: EventUser})
			s.AddEvent(Event{Type: EventAssistant})
			s.Flush()
			if got := eventsOnDisk(t, rec, s.ID); len(got) != 2 {
				t.Errorf("after Flush on disk = %v", got)
			}
			s.AddEvent(Event{Type: EventToolCall})
			s.Close()
			s.Close() // idempotent
			if got := eventsOnDisk(t, rec, s.ID); len(got) != 3 {
				t.Errorf("after Close on disk = %v", got)
			}

			// After Close: AddEvent appends in memory without blocking, Flush
			// is a no-op, and a later Update persists the appended events.
			for range eventChSize + 1 {
				s.AddEvent(Event{Type: EventWarning})
			}
			s.Flush()
			if got := eventsOnDisk(t, rec, s.ID); len(got) != 3 {
				t.Errorf("after Close+AddEvent on disk = %d, want 3 (not persisted yet)", len(got))
			}
			if len(s.events) != 3+eventChSize+1 {
				t.Errorf("in memory = %d events", len(s.events))
			}
			s.Status = StatusComplete
			if err := rec.Update(s); err != nil {
				t.Fatal(err)
			}
			got, _ := rec.Get(s.ID)
			if len(got.events) != 3+eventChSize+1 || got.Status != StatusComplete {
				t.Errorf("after final Update: %d events, status %q", len(got.events), got.Status)
			}
		})
	})
	t.Run("persist error is dropped", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dir := t.TempDir()
			rec := mustOpen(t, dir, nil)
			s, _ := rec.Create(Meta{Name: "wf"})
			os.RemoveAll(dir)
			s.AddEvent(Event{Type: EventUser})
			s.Close()
			if len(s.events) != 1 {
				t.Errorf("events kept in memory = %d, want 1", len(s.events))
			}
		})
	})
}

func TestDetectFormat(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr error
	}{
		{"jsonl ext", write("a.jsonl", "x"), "jsonl", nil},
		{"json ext", write("a.json", "x"), "json", nil},
		{"sniff jsonl", write("a", `{"_type":"header"}`), "jsonl", nil},
		{"sniff json", write("b", `{"id":"x","events":[]}`), "json", nil},
		{"unknown", write("c", `hello`), "", ErrUnknownFormat},
		{"empty", write("d", ``), "", ErrUnknownFormat},
		{"missing", filepath.Join(dir, "nope"), "", os.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectFormat(tt.path)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("DetectFormat(%q) = %q, %v; want %q, %v", tt.path, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}
	long := strings.Repeat("x", 20)
	jsonl := "{\"_type\":\"header\",\"id\":\"j\",\"workflow_name\":\"w\",\"inputs\":{\"a\":\"b\"}}\n" +
		"\n" + // blank line skipped
		"{\"_type\":\"event\"}\n" + // event record without event body: skipped
		"{\"_type\":\"bogus\"}\n" + // unknown record type: ignored
		"{\"_type\":\"event\",\"seq\":1,\"type\":\"user\",\"content\":\"" + long + "\"}\n" +
		"{\"_type\":\"footer\",\"status\":\"running\"}\n" +
		"{\"_type\":\"footer\",\"status\":\"complete\",\"outputs\":{\"o\":\"v\"}}" // no trailing newline
	legacy := `{"id":"l","events":[{"seq":3,"type":"user","content":"` + long + `"}]}`
	os.Mkdir(filepath.Join(dir, "dir.jsonl"), 0o755)

	truncated := long[:5] + "\n... [truncated, 20 bytes total]"
	tests := []struct {
		name    string
		path    string
		opts    ReadOptions
		want    *Session
		wantErr string
	}{
		{name: "jsonl", path: write("j.jsonl", jsonl), want: &Session{
			ID: "j", WorkflowName: "w", Inputs: map[string]string{"a": "b"}, Status: StatusComplete,
			Outputs: map[string]string{"o": "v"}, events: []Event{{SeqID: 1, Type: EventUser, Content: long}},
		}},
		{name: "jsonl truncated", path: write("j.jsonl", jsonl), opts: ReadOptions{MaxContentSize: 5}, want: &Session{
			ID: "j", WorkflowName: "w", Inputs: map[string]string{"a": "b"}, Status: StatusComplete,
			Outputs: map[string]string{"o": "v"}, events: []Event{{SeqID: 1, Type: EventUser, Content: truncated}},
		}},
		{name: "legacy", path: write("l.json", legacy), want: &Session{
			ID: "l", events: []Event{{SeqID: 3, Type: EventUser, Content: long}},
		}},
		{name: "legacy truncated", path: write("l.json", legacy), opts: ReadOptions{MaxContentSize: 5}, want: &Session{
			ID: "l", events: []Event{{SeqID: 3, Type: EventUser, Content: truncated}},
		}},
		{name: "unknown format", path: write("u", "?"), wantErr: "unknown file format"},
		{name: "jsonl missing", path: filepath.Join(dir, "nope.jsonl"), wantErr: "read file"},
		{name: "jsonl is a directory", path: filepath.Join(dir, "dir.jsonl"), wantErr: "read file"},
		{name: "jsonl malformed", path: write("m.jsonl", "{\"_type\":\"header\"}\nnot json\n"), wantErr: "parse JSONL line"},
		{name: "legacy missing", path: filepath.Join(dir, "nope.json"), wantErr: "read file"},
		{name: "legacy malformed", path: write("m.json", "{"), wantErr: "parse legacy JSON"},
	}
	ignore := cmp.Options{cmp.AllowUnexported(Session{}), cmp.FilterPath(func(p cmp.Path) bool {
		return p.Last().String() == ".seq" || p.Last().String() == ".written" || p.Last().String() == ".mu"
	}, cmp.Ignore())}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadFile(tt.path, tt.opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ReadFile(%q) error = %v, want containing %q", tt.path, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadFile(%q) error: %v", tt.path, err)
			}
			if diff := cmp.Diff(tt.want, got, ignore); diff != "" {
				t.Errorf("ReadFile(%q) (-want +got):\n%s", tt.path, diff)
			}
			last := tt.want.events[len(tt.want.events)-1].SeqID
			if got.seq.Load() != last || got.written != len(tt.want.events) {
				t.Errorf("seq=%d written=%d, want %d, %d", got.seq.Load(), got.written, last, len(tt.want.events))
			}
		})
	}
}
