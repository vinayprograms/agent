package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/swarm"
)

// These tests pin the swarm wire format: the JSON field names the CLI, web UI
// and hand-rolled parsers depend on must not change (ledger invariant).

func jsonKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTaskResultWireFieldNames(t *testing.T) {
	r := swarm.NewTaskResult("t-1", "agent-a", swarm.ResultSuccess)
	r.DurationMs = 42
	m := jsonKeys(t, r)
	for _, k := range []string{"task_id", "agent_id", "duration_ms", "status", "attempt", "completed_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("TaskResult JSON missing key %q: %v", k, m)
		}
	}
	if m["task_id"] != "t-1" || m["agent_id"] != "agent-a" || m["status"] != "success" || m["duration_ms"] != float64(42) {
		t.Errorf("unexpected values: %v", m)
	}
}

func TestTaskMessageWireFieldNames(t *testing.T) {
	tm := swarm.NewTaskMessage("t-2", "summarize", map[string]string{"task": "hi"})
	m := jsonKeys(t, tm)
	for _, k := range []string{"task_id", "capability", "inputs", "created_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("TaskMessage JSON missing key %q: %v", k, m)
		}
	}
	back, err := swarm.UnmarshalTaskMessage([]byte(`{"task_id":"x","capability":"c","inputs":{"task":"y"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if back.TaskID != "x" || back.Capability != "c" || back.Inputs["task"] != "y" {
		t.Errorf("round trip mismatch: %+v", back)
	}
}

// TestHeartbeatParsedByHandRolledStructs mirrors the anonymous structs in
// main.go (AgentsCmd, discoverAgentsViaHeartbeat) and tui.go (subscribeCmd).
func TestHeartbeatParsedByHandRolledStructs(t *testing.T) {
	hb := &swarm.Heartbeat{
		AgentID:   "worker-1",
		Timestamp: time.Now(),
		Status:    "busy",
		Load:      0.5,
		Metadata:  map[string]string{"capability": "summarize", "name": "Summarizer"},
	}
	data, err := hb.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		AgentID  string            `json:"agent_id"`
		Status   string            `json:"status"`
		Load     float64           `json:"load"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.AgentID != "worker-1" || parsed.Status != "busy" || parsed.Load != 0.5 || parsed.Metadata["capability"] != "summarize" {
		t.Errorf("hand-rolled heartbeat parse mismatch: %+v", parsed)
	}
	if !strings.Contains(string(data), `"agent_id"`) {
		t.Errorf("heartbeat JSON must use agent_id: %s", data)
	}
	back, err := swarm.UnmarshalHeartbeat(data)
	if err != nil || back.AgentID != "worker-1" {
		t.Errorf("UnmarshalHeartbeat: %v %+v", err, back)
	}
}

// TestResultDiscriminator pins the `AgentID != ""` check used by web.go to
// tell a TaskResult from a TaskMessage on discuss.*.
func TestResultDiscriminator(t *testing.T) {
	var asResult swarm.TaskResult
	msg, _ := swarm.NewTaskMessage("t", "c", map[string]string{"task": "x"}).Marshal()
	if err := json.Unmarshal(msg, &asResult); err != nil || asResult.AgentID != "" {
		t.Errorf("TaskMessage must not look like a TaskResult: err=%v agent=%q", err, asResult.AgentID)
	}
	res, _ := swarm.NewTaskResult("t", "a", swarm.ResultFailed).Marshal()
	asResult = swarm.TaskResult{}
	if err := json.Unmarshal(res, &asResult); err != nil || asResult.AgentID == "" {
		t.Errorf("TaskResult must carry agent_id: err=%v", err)
	}
}

func TestPrintResultOutputKeys(t *testing.T) {
	r := &swarm.TaskResult{TaskID: "t-9", Status: swarm.ResultFailed, Error: "boom", DurationMs: 7, Outputs: map[string]any{"a": 1}}
	out := captureStdout(t, func() {
		if err := printResult(r); err != nil {
			t.Error(err)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("printResult output not JSON: %v\n%s", err, out)
	}
	if m["task_id"] != "t-9" || m["status"] != "failed" || m["error"] != "boom" || m["duration_ms"] != float64(7) {
		t.Errorf("unexpected printResult output: %s", out)
	}
}

// TestLegacyOnDiskTaskFilesReadable writes files in the exact schema older
// builds wrote to ~/.swarm/tasks/*.json and checks the DB still reads them.
func TestLegacyOnDiskTaskFilesReadable(t *testing.T) {
	db, dir := newTestDB(t)
	tasks := filepath.Join(dir, "tasks")
	os.MkdirAll(tasks, 0o755)
	legacyResult := `{"task_id":"t-old","status":"success","outputs":{"x":"y"},"agent_id":"w1","attempt":1,"duration_ms":1234,"completed_at":"2025-01-02T03:04:05Z"}`
	legacyInput := `{"task_id":"t-old","capability":"summarize","attempt":1,"inputs":{"task":"do it"},"created_at":"2025-01-02T03:04:00Z","submitted_at":"2025-01-02T03:04:00Z"}`
	os.WriteFile(filepath.Join(tasks, "t-old.json"), []byte(legacyResult), 0o644)
	os.WriteFile(filepath.Join(tasks, "t-old.input.json"), []byte(legacyInput), 0o644)

	res, err := db.GetResult("t-old")
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t-old" || res.AgentID != "w1" || res.DurationMs != 1234 || res.Status != swarm.ResultSuccess {
		t.Errorf("legacy result mismatch: %+v", res)
	}
	in, err := db.GetTask("t-old")
	if err != nil {
		t.Fatal(err)
	}
	if in.TaskID != "t-old" || in.Capability != "summarize" || in.Inputs["task"] != "do it" {
		t.Errorf("legacy input mismatch: %+v", in)
	}
}

func TestStaticIndexUsesWireFieldNames(t *testing.T) {
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, k := range []string{"agent_id", "task_id", "duration_ms"} {
		if !strings.Contains(html, k) {
			t.Errorf("static/index.html no longer references wire field %q", k)
		}
	}
}
