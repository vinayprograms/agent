package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agent/internal/swarm"
	"golang.org/x/net/websocket"
)

func newTestWebServer(t *testing.T, withDB bool) (*webServer, *nats.Conn) {
	t.Helper()
	nc, url := startNATS(t)
	var db *taskDB
	if withDB {
		db, _ = newTestDB(t)
	}
	s := newWebServer(url, t.TempDir(), "", nil, db)
	s.nc = nc
	return s, nc
}

func TestCacheMessage(t *testing.T) {
	s := newWebServer("", "", "", nil, nil)
	s.cacheMessage("heartbeat", "heartbeat.w1", []byte("hb"))
	s.cacheMessage("work", "work.cap.t-1", []byte("w"))
	s.cacheMessage("discuss", "discuss.d-1", []byte("d"))
	for i := range maxCachedLogs + 10 {
		s.cacheMessage("log", "log.w1", []byte{byte(i)})
	}
	s.cacheMessage("events", "events.x", []byte("e"))
	if string(s.lastHeartbeats["w1"]) != "hb" || string(s.activeTasks["t-1"]) != "w" || string(s.activeTasks["d-1"]) != "d" {
		t.Errorf("cache: %v %v", s.lastHeartbeats, s.activeTasks)
	}
	if len(s.recentLogs) != maxCachedLogs {
		t.Errorf("ring buffer size %d", len(s.recentLogs))
	}
	s.cacheMessage("done", "done.cap.t-1", nil)
	if _, ok := s.activeTasks["t-1"]; ok {
		t.Error("done should clear active task")
	}
	s.cacheMessage("control", "control.x", nil) // no-op
}

func TestPersistNATSMessageWorkAndDone(t *testing.T) {
	db, _ := newTestDB(t)
	s := newWebServer("", "", "", nil, db)

	tm := swarm.NewTaskMessage("t-1", "cap", map[string]string{"task": "do it"})
	tm.SubmittedBy = "human"
	data, _ := tm.Marshal()
	s.persistNATSMessage("work", "work.cap.t-1", data)
	s.persistNATSMessage("work", "work.cap.t-1", []byte("garbage"))
	if _, err := db.GetTask("t-1"); err != nil {
		t.Fatalf("task not persisted: %v", err)
	}
	thread, _ := db.GetThread("t-1")
	if len(thread) != 1 || thread[0].Type != "topic" || thread[0].Content != "do it" {
		t.Errorf("topic entry: %+v", thread)
	}

	res := swarm.NewTaskResult("t-1", "w1", swarm.ResultFailed)
	res.Outputs = "it broke"
	rdata, _ := res.Marshal()
	s.persistNATSMessage("done", "done.cap.t-1", rdata)
	s.persistNATSMessage("done", "done.cap.t-1", []byte("garbage"))
	thread, _ = db.GetThread("t-1")
	if len(thread) != 2 || thread[1].Type != "error" || thread[1].Content != "it broke" {
		t.Errorf("error entry: %+v", thread)
	}

	// Result without task_id falls back to the subject; non-string outputs marshalled.
	s.persistNATSMessage("done", "done.cap.t-2", []byte(`{"agent_id":"w1","status":"success","outputs":{"k":1}}`))
	thread, _ = db.GetThread("t-2")
	if len(thread) != 1 || thread[0].Type != "result" || thread[0].Content != `{"k":1}` {
		t.Errorf("subject fallback: %+v", thread)
	}

	// nil db is a no-op.
	newWebServer("", "", "", nil, nil).persistNATSMessage("work", "work.cap.t", data)
}

func TestPersistDiscussContribution(t *testing.T) {
	db, _ := newTestDB(t)
	s := newWebServer("", "", "", nil, db)

	s.persistNATSMessage("discuss", "nodots", []byte("{}")) // too few parts

	// Initial topic (TaskMessage without "task" key uses first input).
	tm := swarm.NewTaskMessage("d-1", "", map[string]string{"question": "why?"})
	data, _ := tm.Marshal()
	s.persistNATSMessage("discuss", "discuss.d-1", data)

	// Agent results with various metadata types.
	mk := func(meta map[string]string, out any) []byte {
		r := swarm.NewTaskResult("d-1", "w1", swarm.ResultSuccess)
		r.Metadata = meta
		r.Outputs = out
		b, _ := r.Marshal()
		return b
	}
	s.persistNATSMessage("discuss", "discuss.d-1", mk(map[string]string{"type": "comment", "capability": "c", "name": "W"}, "a comment"))
	s.persistNATSMessage("discuss", "discuss.d-1", mk(map[string]string{"type": "deliberation", "signal": "CLAIM"}, "mine"))
	s.persistNATSMessage("discuss", "discuss.d-1", mk(map[string]string{"type": "deliberation"}, "hmm"))
	s.persistNATSMessage("discuss", "discuss.d-1", mk(nil, map[string]string{"x": "y"}))

	// Skipped variants.
	for _, meta := range []map[string]string{{"prior_output": "p"}, {"type": "human_reply"}, {"type": "synthesis_request"}} {
		m := swarm.NewTaskMessage("d-1", "", map[string]string{"task": "skip"})
		m.Metadata = meta
		b, _ := m.Marshal()
		s.persistNATSMessage("discuss", "discuss.d-1", b)
	}
	s.persistNATSMessage("discuss", "discuss.d-1", []byte("garbage"))

	thread, _ := db.GetThread("d-1")
	types := make([]string, len(thread))
	for i, e := range thread {
		types[i] = e.Type
	}
	want := "topic,comment,claim,deliberation,execute"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("thread types = %s, want %s", got, want)
	}
	if thread[0].Content != "why?" || thread[1].Capability != "c" || thread[1].Name != "W" || thread[4].Content != `{"x":"y"}` {
		t.Errorf("thread contents: %+v", thread)
	}
	if rec, err := db.GetTask("d-1"); err != nil || rec.Inputs["question"] != "why?" {
		t.Errorf("discuss topic not inserted as task: %v", err)
	}
}

func TestHandleTaskDetail(t *testing.T) {
	s, _ := newTestWebServer(t, true)
	s.db.InsertTask(swarm.NewTaskMessage("t-1", "cap", map[string]string{"task": "x"}), "pending")
	s.db.UpdateResult(swarm.NewTaskResult("t-1", "w1", swarm.ResultSuccess))

	get := func(srv *webServer, path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleTaskDetail(rr, httptest.NewRequest("GET", path, nil))
		return rr
	}
	if rr := get(s, "/api/task/"); rr.Code != 400 {
		t.Errorf("missing id: %d", rr.Code)
	}
	if rr := get(newWebServer("", "", "", nil, nil), "/api/task/t-1"); rr.Code != 500 {
		t.Errorf("no db: %d", rr.Code)
	}
	rr := get(s, "/api/task/t-1.json")
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	var body struct {
		TaskID string             `json:"task_id"`
		Input  *swarm.TaskMessage `json:"input"`
		Result *swarm.TaskResult  `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TaskID != "t-1" || body.Input.Capability != "cap" || body.Result.AgentID != "w1" {
		t.Errorf("body: %s", rr.Body.String())
	}
	// Unknown task still answers 200 with nulls.
	rr = get(s, "/api/task/unknown")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"input":null`) {
		t.Errorf("unknown task: %d %s", rr.Code, rr.Body.String())
	}
}

func TestHandleThreadAPI(t *testing.T) {
	s, _ := newTestWebServer(t, true)
	s.db.AppendThread("d-1", threadEntry{AgentID: "human", Type: "topic", Content: "hi"})
	get := func(srv *webServer, path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleThreadAPI(rr, httptest.NewRequest("GET", path, nil))
		return rr
	}
	if rr := get(s, "/api/thread/"); rr.Code != 400 {
		t.Errorf("missing id: %d", rr.Code)
	}
	if rr := get(newWebServer("", "", "", nil, nil), "/api/thread/d-1"); rr.Code != 500 {
		t.Errorf("no db: %d", rr.Code)
	}
	if rr := get(s, "/api/thread/d-1/"); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"content":"hi"`) {
		t.Errorf("thread: %d %s", rr.Code, rr.Body.String())
	}
	if rr := get(s, "/api/thread/none"); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("empty thread: %d %q", rr.Code, rr.Body.String())
	}
}

func TestHandleHumanReply(t *testing.T) {
	s, nc := newTestWebServer(t, true)
	sub, _ := nc.SubscribeSync("discuss.>")
	s.db.AppendThread("d-1", threadEntry{AgentID: "w1", Capability: "cap", Type: "comment", Content: "prior"})

	post := func(path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.handleHumanReply(rr, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return rr
	}
	rr := httptest.NewRecorder()
	s.handleHumanReply(rr, httptest.NewRequest("GET", "/api/reply/d-1", nil))
	if rr.Code != 405 {
		t.Errorf("GET: %d", rr.Code)
	}
	if rr := post("/api/reply/", `{"message":"x"}`); rr.Code != 400 {
		t.Errorf("missing id: %d", rr.Code)
	}
	if rr := post("/api/reply/d-1", `{"message":""}`); rr.Code != 400 {
		t.Errorf("empty message: %d", rr.Code)
	}
	if rr := post("/api/reply/d-1", `nope`); rr.Code != 400 {
		t.Errorf("bad json: %d", rr.Code)
	}

	// Plain reply with a target.
	if rr := post("/api/reply/d-1/", `{"message":"hello","target":"w1"}`); rr.Code != 200 {
		t.Fatalf("reply: %d %s", rr.Code, rr.Body.String())
	}
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := swarm.UnmarshalTaskMessage(msg.Data)
	if err != nil || msg.Subject != "discuss.d-1" || tm.Metadata["type"] != "human_reply" || tm.Metadata["target_agent"] != "w1" || tm.Inputs["task"] != "hello" {
		t.Errorf("published reply: %s %+v %v", msg.Subject, tm, err)
	}

	// Synthesis request embeds the thread.
	if rr := post("/api/reply/d-1", `{"message":"wrap up","synthesize":true}`); rr.Code != 200 {
		t.Fatalf("synth: %d", rr.Code)
	}
	msg, _ = sub.NextMsg(2 * time.Second)
	tm, _ = swarm.UnmarshalTaskMessage(msg.Data)
	if tm.Metadata["type"] != "synthesis_request" || !strings.Contains(tm.Inputs["task"], "[cap] (comment):\nprior") || !strings.Contains(tm.Inputs["task"], "SYNTHESIS REQUEST:\nwrap up") {
		t.Errorf("synthesis payload: %+v", tm)
	}
	thread, _ := s.db.GetThread("d-1")
	if len(thread) != 3 || thread[1].Type != "human" {
		t.Errorf("thread: %+v", thread)
	}

	// Publish failure → 500.
	nc.Close()
	if rr := post("/api/reply/d-1", `{"message":"x"}`); rr.Code != 500 {
		t.Errorf("closed conn: %d", rr.Code)
	}
}

func sessionsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "alpha", "sessions", "label1")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte("{\"a\":1}\n\n{\"b\":2}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "agents", "stray-file-holder"), 0o755)
	os.WriteFile(filepath.Join(root, "agents", "file"), nil, 0o644)
	return root
}

func TestHandleListSessions(t *testing.T) {
	get := func(srv *webServer, path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleListSessions(rr, httptest.NewRequest("GET", path, nil))
		return rr
	}
	s := newWebServer("", "", "", nil, nil)
	if rr := get(s, "/api/sessions/x"); rr.Code != 404 {
		t.Errorf("wrong path: %d", rr.Code)
	}
	if rr := get(s, "/api/sessions"); rr.Code != 503 {
		t.Errorf("no root: %d", rr.Code)
	}
	s.storageRoot = t.TempDir()
	if rr := get(s, "/api/sessions"); rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "null" {
		t.Errorf("empty root: %d %q", rr.Code, rr.Body.String())
	}
	s.storageRoot = sessionsFixture(t)
	rr := get(s, "/api/sessions")
	var list []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list: %v %s", err, rr.Body.String())
	}
	if list[0]["agent"] != "alpha" || list[0]["label"] != "label1" || list[0]["session_id"] != "s1" {
		t.Errorf("entry: %v", list[0])
	}
}

func TestHandleSessionLogs(t *testing.T) {
	get := func(srv *webServer, path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		srv.handleSessionLogs(rr, httptest.NewRequest("GET", path, nil))
		return rr
	}
	s := newWebServer("", "", "", nil, nil)
	if rr := get(s, "/api/sessions/alpha/s1"); rr.Code != 503 {
		t.Errorf("no root: %d", rr.Code)
	}
	s.storageRoot = sessionsFixture(t)
	cases := map[string]int{
		"/api/sessions/alpha":         400,
		"/api/sessions/alpha/":        400,
		"/api/sessions/../x/s1":       400,
		"/api/sessions/alpha/..":      400,
		"/api/sessions/alpha/s1":      200,
		"/api/sessions/alpha/s1/":     200,
		"/api/sessions/other/s1":      200, // fallback scan across agents
		"/api/sessions/alpha/missing": 404,
		"/api/sessions/ghost/missing": 404,
	}
	for path, want := range cases {
		if rr := get(s, path); rr.Code != want {
			t.Errorf("%s: got %d want %d (%s)", path, rr.Code, want, rr.Body.String())
		}
	}
	rr := get(s, "/api/sessions/alpha/s1")
	var recs []json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &recs); err != nil || len(recs) != 2 {
		t.Errorf("records: %v %s", err, rr.Body.String())
	}
}

func TestBroadcastPersistsAndCaches(t *testing.T) {
	s, _ := newTestWebServer(t, true)
	tm := swarm.NewTaskMessage("t-1", "cap", map[string]string{"task": "x"})
	data, _ := tm.Marshal()
	s.broadcast("work.>", &nats.Msg{Subject: "work.cap.t-1", Data: data})
	if _, ok := s.activeTasks["t-1"]; !ok {
		t.Error("not cached")
	}
	if _, err := s.db.GetTask("t-1"); err != nil {
		t.Error("not persisted")
	}
	var ws wsMessage
	if err := json.Unmarshal(s.activeTasks["t-1"], &ws); err != nil || ws.Type != "work" || ws.Subject != "work.cap.t-1" {
		t.Errorf("wsMessage envelope: %+v %v", ws, err)
	}
}

func TestHandleCommandsPublish(t *testing.T) {
	s, nc := newTestWebServer(t, true)
	work, _ := nc.SubscribeSync("work.>")
	discuss, _ := nc.SubscribeSync("discuss.>")
	control, _ := nc.SubscribeSync("control.>")
	nc.Flush()

	s.handleCommand("", nil)
	s.handleCommand("/clear", nil)
	s.handleCommand("/bogus", nil)
	s.handleCommand("/task onlycap", nil)
	s.handleCommand("/discuss onlycap", nil)
	s.handleCommand("/retry", nil)
	s.handleCommand("/retry unknown-id", nil)

	s.handleCommand(`/task summarize "hello world"`, nil)
	msg, err := work.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tm, _ := swarm.UnmarshalTaskMessage(msg.Data)
	if !strings.HasPrefix(msg.Subject, "work.summarize.t-") || tm.Inputs["task"] != "hello world" || tm.Capability != "summarize" {
		t.Errorf("task publish: %s %+v", msg.Subject, tm)
	}
	if _, err := s.db.GetTask(tm.TaskID); err != nil {
		t.Error("task not recorded")
	}

	s.handleCommand("/discuss all topic here", nil)
	msg, err = discuss.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dm, _ := swarm.UnmarshalTaskMessage(msg.Data)
	if !strings.HasPrefix(msg.Subject, "discuss.d-") || dm.Inputs["task"] != "topic here" {
		t.Errorf("discuss publish: %s %+v", msg.Subject, dm)
	}

	s.handleCommand("/retry "+tm.TaskID, nil)
	msg, err = work.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var retry map[string]any
	json.Unmarshal(msg.Data, &retry)
	if retry["retry_of"] != tm.TaskID || retry["capability"] != "summarize" {
		t.Errorf("retry publish: %s %s", msg.Subject, msg.Data)
	}

	savePIDRecords(s.dataDir, []pidRecord{{Name: "w1", PID: os.Getpid()}})
	s.handleCommand("/shutdown", nil)
	msg, err = control.NextMsg(2 * time.Second)
	if err != nil || msg.Subject != "control.w1.shutdown" {
		t.Errorf("shutdown: %v %v", err, msg)
	}

	// Deliver a result for the task so the pending done-subscriber goroutine completes.
	res, _ := swarm.NewTaskResult(tm.TaskID, "w1", swarm.ResultSuccess).Marshal()
	nc.Publish("done.summarize."+tm.TaskID, res)
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	// Retry/result with no db are no-ops.
	noDB := newWebServer("", "", "", nil, nil)
	noDB.handleRetryCommand([]string{"x"})
	noDB.handleResultCommand([]string{"x"}, nil)
	noDB.handleResultCommand(nil, nil)
}

func TestWebSocketInitialStateAndResult(t *testing.T) {
	s, _ := newTestWebServer(t, true)
	s.db.InsertTask(swarm.NewTaskMessage("t-1", "cap", map[string]string{"task": "x"}), "pending")
	s.db.UpdateResult(swarm.NewTaskResult("t-1", "w1", swarm.ResultSuccess))
	s.cacheMessage("heartbeat", "heartbeat.w1", []byte(`{"type":"heartbeat"}`))
	s.cacheMessage("work", "work.cap.t-2", []byte(`{"type":"work"}`))
	s.cacheMessage("log", "log.w1", []byte(`{"type":"log"}`))

	srv := httptest.NewServer(websocket.Handler(s.handleWS))
	defer srv.Close()
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	recv := func() map[string]any {
		var raw string
		ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			t.Fatalf("receive: %v", err)
		}
		var m map[string]any
		json.Unmarshal([]byte(raw), &m)
		return m
	}
	var types []string
	for range 4 { // heartbeat, work, log, history
		types = append(types, recv()["type"].(string))
	}
	if got := strings.Join(types, ","); got != "heartbeat,work,log,history" {
		t.Errorf("initial state order: %s", got)
	}

	websocket.Message.Send(ws, "not json")
	websocket.Message.Send(ws, `{"command":"/result t-1"}`)
	m := recv()
	if m["type"] != "result_detail" {
		t.Errorf("result_detail: %v", m)
	}
	var res swarm.TaskResult
	b, _ := json.Marshal(m["data"])
	json.Unmarshal(b, &res)
	if res.AgentID != "w1" {
		t.Errorf("result payload: %s", b)
	}

	// A broadcast now reaches the connected client.
	s.broadcast("log.>", &nats.Msg{Subject: "log.w1", Data: []byte(`{"line":"x"}`)})
	if m := recv(); m["type"] != "log" {
		t.Errorf("broadcast: %v", m)
	}
	ws.Close()
	time.Sleep(50 * time.Millisecond)
	s.mu.RLock()
	n := len(s.clients)
	s.mu.RUnlock()
	if n != 0 {
		t.Errorf("client not removed: %d", n)
	}
}

func TestWebServerStartAndServe(t *testing.T) {
	_, url := startNATS(t)
	db, _ := newTestDB(t)
	s := newWebServer(url, t.TempDir(), "", nil, db)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.start(ctx, "127.0.0.1:0") }()
	// start() blocks; hit it through its NATS side instead of the listener
	// (the chosen port is not exported). Just ensure it returns on ctx cancel.
	select {
	case err := <-done:
		t.Fatalf("start returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not return after cancel")
	}
	if err := newWebServer("nats://127.0.0.1:1", "", "", nil, nil).start(ctx, "127.0.0.1:0"); err == nil || !strings.Contains(err.Error(), "NATS connect") {
		t.Errorf("bad nats url: %v", err)
	}
}

func TestHandleCommandsTaskResultBroadcastPath(t *testing.T) {
	s, nc := newTestWebServer(t, true)
	work, _ := nc.SubscribeSync("work.>")
	nc.Flush()
	s.handleTaskCommand([]string{"cap", "go"})
	msg, _ := work.NextMsg(2 * time.Second)
	tm, _ := swarm.UnmarshalTaskMessage(msg.Data)
	res, _ := swarm.NewTaskResult(tm.TaskID, "w1", swarm.ResultSuccess).Marshal()
	nc.Publish("done.cap."+tm.TaskID, res)
	nc.Flush()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := s.db.GetResult(tm.TaskID); err == nil && r.AgentID == "w1" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("result from done-subscriber goroutine not persisted")
}
