package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/swarm"
)

func TestTaskDBInsertGetUpdate(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	tm := swarm.NewTaskMessage("t-1", "summarize", map[string]string{"task": "x"})
	if err := db.InsertTask(tm, "pending"); err != nil {
		t.Fatal(err)
	}
	// Duplicate insert is a no-op.
	if err := db.InsertTask(tm, "running"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTask("t-1")
	if err != nil || got.Capability != "summarize" {
		t.Fatalf("GetTask: %v %+v", err, got)
	}
	if _, err := db.GetTask("nope"); err == nil {
		t.Error("GetTask missing should error")
	}
	if _, err := db.GetResult("t-1"); err == nil {
		t.Error("GetResult before result should error")
	}

	res := swarm.NewTaskResult("t-1", "w1", swarm.ResultSuccess)
	res.DurationMs = 99
	if err := db.UpdateResult(res); err != nil {
		t.Fatal(err)
	}
	back, err := db.GetResult("t-1")
	if err != nil || back.DurationMs != 99 || back.AgentID != "w1" {
		t.Fatalf("GetResult: %v %+v", err, back)
	}
	recs, _ := db.ListTasks("", "", 10)
	if len(recs) != 1 || recs[0].Status != "success" || recs[0].DurationMs != 99 {
		t.Errorf("record not updated: %+v", recs)
	}

	// Corrupt result file → GetResult errors.
	os.WriteFile(filepath.Join(filepath.Dir(db.dbPath), "tasks", "bad.json"), []byte("{"), 0o644)
	if _, err := db.GetResult("bad"); err == nil {
		t.Error("corrupt result should error")
	}
}

func TestTaskDBListAndStats(t *testing.T) {
	db, _ := newTestDB(t)
	statuses := []string{"pending", "running", "success", "failed", "success"}
	for i, st := range statuses {
		cap := "a"
		if i%2 == 1 {
			cap = "b"
		}
		if err := db.InsertTask(swarm.NewTaskMessage("t-"+string(rune('0'+i)), cap, nil), st); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct CreatedAt for ordering
	}
	all, _ := db.ListTasks("", "", 100)
	if len(all) != 5 || all[0].TaskID != "t-4" {
		t.Errorf("want newest first, got %+v", all)
	}
	if got, _ := db.ListTasks("b", "", 100); len(got) != 2 {
		t.Errorf("capability filter: %d", len(got))
	}
	if got, _ := db.ListTasks("", "success", 100); len(got) != 2 {
		t.Errorf("status filter: %d", len(got))
	}
	if got, _ := db.ListTasks("", "", 2); len(got) != 2 {
		t.Errorf("limit: %d", len(got))
	}
	s, _ := db.Stats()
	if s.Total != 5 || s.Success != 2 || s.Failed != 1 || s.Pending != 1 || s.Running != 1 {
		t.Errorf("stats: %+v", s)
	}
}

func TestTaskDBThread(t *testing.T) {
	db, dir := newTestDB(t)
	if _, err := db.GetThread("none"); err == nil {
		t.Error("missing thread should error")
	}
	e1 := threadEntry{AgentID: "human", Type: "topic", Content: "hello", Timestamp: time.Now()}
	e2 := threadEntry{AgentID: "w1", Capability: "c", Name: "W", Type: "comment", Content: "reply", Round: 1}
	if err := db.AppendThread("d-1", e1); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendThread("d-1", e2); err != nil {
		t.Fatal(err)
	}
	// A garbage line is skipped, not fatal.
	f, _ := os.OpenFile(filepath.Join(dir, "tasks", "d-1.thread.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("not json\n")
	f.Close()
	got, err := db.GetThread("d-1")
	if err != nil || len(got) != 2 || got[0].Content != "hello" || got[1].Name != "W" {
		t.Errorf("GetThread: %v %+v", err, got)
	}
}

func TestTaskDBLoadRecordsMissingOrCorrupt(t *testing.T) {
	db, dir := newTestDB(t)
	if recs := db.loadRecords(); recs != nil {
		t.Errorf("missing file should yield nil, got %v", recs)
	}
	os.WriteFile(filepath.Join(dir, "tasks.json"), []byte("{bad"), 0o644)
	if recs := db.loadRecords(); len(recs) != 0 {
		t.Errorf("corrupt file should yield empty, got %v", recs)
	}
}

func TestPIDRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if got := loadPIDRecords(dir); got != nil {
		t.Errorf("no file: %v", got)
	}
	recs := []pidRecord{
		{Name: "self", PID: os.Getpid(), Capability: "c", StartedAt: "now"},
		{Name: "dead", PID: 1 << 30},
	}
	if err := savePIDRecords(dir, recs); err != nil {
		t.Fatal(err)
	}
	loaded := loadPIDRecords(dir)
	if len(loaded) != 2 || loaded[0].Name != "self" {
		t.Errorf("loadPIDRecords: %+v", loaded)
	}
	alive := cleanStalePIDs(loaded)
	if len(alive) != 1 || alive[0].Name != "self" {
		t.Errorf("cleanStalePIDs: %+v", alive)
	}
	if isProcessAlive(-1) {
		t.Error("pid -1 reported alive")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("short: %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Errorf("long: %q", got)
	}
}

func TestClassifySubject(t *testing.T) {
	cases := map[string]string{
		"heartbeat.w1": "heartbeat", "work.c.t": "work", "done.c.t": "done", "discuss.t": "discuss",
		"control.w1.shutdown": "control", "log.w1": "log", "events.x": "events", "other": "unknown",
	}
	for subj, want := range cases {
		if got := classifySubject(subj); got != want {
			t.Errorf("classifySubject(%q) = %q want %q", subj, got, want)
		}
	}
}

func TestListTree(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755)
	os.WriteFile(filepath.Join(root, "a", "f1"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "a", "b", "c", "deep"), nil, 0o644)
	files, err := listTree(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, ",")
	if !strings.Contains(joined, "a/f1") || strings.Contains(joined, "deep") {
		t.Errorf("listTree depth handling: %v", files)
	}
	if files, _ := listTree(filepath.Join(root, "missing"), 1); len(files) != 0 {
		t.Errorf("missing root: %v", files)
	}
}

func TestExecCmd(t *testing.T) {
	if err := execCmd("true"); err != nil {
		t.Errorf("true: %v", err)
	}
	if err := execCmd("false"); err == nil {
		t.Error("false should fail")
	}
}

func TestPrefixLines(t *testing.T) {
	nc, _ := startNATS(t)
	sub, _ := nc.SubscribeSync("log.w1")
	var out strings.Builder
	prefixLines("w1", "cap", strings.NewReader("line one\nline two"), &out, nc)
	if out.String() != "[w1] line one\n[w1] line two\n" {
		t.Errorf("prefixLines output: %q", out.String())
	}
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil || !strings.Contains(string(msg.Data), `"line":"line one"`) {
		t.Errorf("nats log publish: %v %s", err, msg.Data)
	}
	out.Reset()
	prefixLines("w2", "", strings.NewReader("x\n"), &out, nil)
	if out.String() != "[w2] x\n" {
		t.Errorf("nil nc: %q", out.String())
	}
}

func TestReplayTUIView(t *testing.T) {
	r := &replayTUI{taskID: "t", events: []replayEvent{
		{Agent: "a", Type: "goal", Message: "g", Status: "success"},
		{Agent: "a", Type: "tool", Message: "f", Status: "failed"},
		{Agent: "a", Type: "tool", Message: "r", Status: "running"},
	}}
	v := r.View()
	for _, want := range []string{"─── a ───", "✓ goal: g", "✗ tool: f", "⏳ tool: r"} {
		if !strings.Contains(v, want) {
			t.Errorf("View missing %q:\n%s", want, v)
		}
	}
}

func TestReplayTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := replayTask("missing"); err == nil || !strings.Contains(err.Error(), "task not found") {
		t.Errorf("missing: %v", err)
	}
	dir := filepath.Join(home, ".local", "share", "swarm", "tasks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644)
	if err := replayTask("bad"); err == nil || !strings.Contains(err.Error(), "parse result") {
		t.Errorf("bad json: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "t-1.json"),
		[]byte(`{"task_id":"t-1","status":"failed","error":"boom","duration_ms":12,"outputs":{"k":"v"}}`), 0o644)
	out := captureStdout(t, func() {
		if err := replayTask("t-1"); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"Task: t-1", "Status: failed", "Duration: 12ms", "Error: boom", `"k": "v"`} {
		if !strings.Contains(out, want) {
			t.Errorf("replayTask output missing %q:\n%s", want, out)
		}
	}
}

func TestReplayWebErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := replayWeb("missing"); err == nil || !strings.Contains(err.Error(), "task not found") {
		t.Errorf("missing: %v", err)
	}
	dir := filepath.Join(home, ".local", "share", "swarm", "tasks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644)
	if err := replayWeb("bad"); err == nil || !strings.Contains(err.Error(), "parse result") {
		t.Errorf("bad json: %v", err)
	}
	// NOTE: the happy path is not exercised: replayWeb does
	// result["duration_ms"].(int64) on a map[string]interface{} decoded by
	// encoding/json, which always yields float64 and therefore panics.
	// Recorded as a parked smell for the refactor phase.
}
