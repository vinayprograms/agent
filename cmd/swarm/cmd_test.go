package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agent/internal/swarm"
)

// beat publishes heartbeats for agentID every 100ms until ctx is done.
func beat(ctx context.Context, nc *nats.Conn, agentID string, meta map[string]string) {
	hb := &swarm.Heartbeat{AgentID: agentID, Status: "idle", Load: 0.1, Metadata: meta}
	data, _ := hb.Marshal()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		nc.Publish("heartbeat."+agentID, data)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func TestConnectError(t *testing.T) {
	a := newTestApp(t, "nats://127.0.0.1:1")
	if _, err := a.connect(); err == nil || !strings.Contains(err.Error(), "connect nats") {
		t.Errorf("connect: %v", err)
	}
	for name, run := range map[string]func() error{
		"status":       func() error { return (&StatusCmd{}).Run(a) },
		"agents":       func() error { return (&AgentsCmd{}).Run(a) },
		"capabilities": func() error { return (&CapabilitiesCmd{}).Run(a) },
		"submit":       func() error { return (&SubmitCmd{Capability: "c", Task: "x"}).Run(a) },
		"discuss":      func() error { return (&DiscussCmd{Task: "x"}).Run(a) },
		"result":       func() error { return (&ResultCmd{TaskID: "t"}).Run(a) },
		"purge":        func() error { return (&PurgeCmd{}).Run(a) },
	} {
		if err := run(); err == nil {
			t.Errorf("%s: expected connect error", name)
		}
	}
}

func TestStatusAndHistoryCmds(t *testing.T) {
	_, url := startNATS(t)
	a := newTestApp(t, url)
	db, _ := a.db()
	db.InsertTask(swarm.NewTaskMessage("t-1", "cap", nil), "pending")
	db.InsertTask(swarm.NewTaskMessage("t-2", "other", nil), "success")

	out := captureStdout(t, func() {
		if err := (&StatusCmd{}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Status: connected") || !strings.Contains(out, "Tasks: 2 total (1 success, 0 failed, 1 pending, 0 running)") {
		t.Errorf("status output:\n%s", out)
	}

	out = captureStdout(t, func() {
		if err := (&HistoryCmd{Limit: 10}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "t-1\tcap\tpending") || !strings.Contains(out, "t-2\tother\tsuccess") {
		t.Errorf("history output:\n%s", out)
	}
	out = captureStdout(t, func() { (&HistoryCmd{Capability: "none", Limit: 10}).Run(a) })
	if !strings.Contains(out, "No tasks found") {
		t.Errorf("empty history:\n%s", out)
	}
}

func TestAgentsAndCapabilitiesCmds(t *testing.T) {
	nc, url := startNATS(t)
	a := newTestApp(t, url)

	out := captureStdout(t, func() { (&AgentsCmd{}).Run(a) })
	if !strings.Contains(out, "No agents discovered") {
		t.Errorf("no agents:\n%s", out)
	}
	out = captureStdout(t, func() { (&CapabilitiesCmd{}).Run(a) })
	if !strings.Contains(out, "No capabilities discovered") {
		t.Errorf("no caps:\n%s", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go beat(ctx, nc, "w1", map[string]string{"capability": "summarize", "name": "Summarizer"})
	go beat(ctx, nc, "w2", map[string]string{"capability": "summarize"})
	go beat(ctx, nc, "w3", nil)
	nc.Publish("heartbeat.bad", []byte("not json"))

	out = captureStdout(t, func() {
		if err := (&AgentsCmd{}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "w1") || !strings.Contains(out, "w2") || !strings.Contains(out, "summarize") {
		t.Errorf("agents output:\n%s", out)
	}
	out = captureStdout(t, func() {
		if err := (&CapabilitiesCmd{}).Run(a); err != nil {
			t.Error(err)
		}
	})
	// NOTE: the count is heartbeat messages, not distinct agents (parked smell).
	if !strings.Contains(out, "summarize\t(") {
		t.Errorf("capabilities output:\n%s", out)
	}

	agents := discoverAgentsViaHeartbeat(nc, 500*time.Millisecond)
	names := map[string]string{}
	for _, ag := range agents {
		names[ag.id] = ag.name
	}
	if len(agents) != 3 || names["w1"] != "Summarizer" || names["w3"] != "w3" {
		t.Errorf("discover: %+v", agents)
	}
	closed, _ := nats.Connect(url)
	closed.Close()
	if got := discoverAgentsViaHeartbeat(closed, time.Millisecond); got != nil {
		t.Errorf("closed conn: %v", got)
	}
}

func TestSubmitCmd(t *testing.T) {
	nc, url := startNATS(t)
	a := newTestApp(t, url)
	work, _ := nc.SubscribeSync("work.>")
	nc.Flush()

	next := func() *swarm.TaskMessage {
		msg, err := work.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatal(err)
		}
		tm, _ := swarm.UnmarshalTaskMessage(msg.Data)
		return tm
	}

	// Positional plain text → task input.
	out := captureStdout(t, func() {
		if err := (&SubmitCmd{Capability: "cap", Task: "do it", NoWait: true}).Run(a); err != nil {
			t.Error(err)
		}
	})
	tm := next()
	if tm.Inputs["task"] != "do it" || tm.Capability != "cap" || strings.TrimSpace(out) != tm.TaskID {
		t.Errorf("plain submit: %+v out=%q", tm, out)
	}
	db, _ := a.db()
	if _, err := db.GetTask(tm.TaskID); err != nil {
		t.Error("submit not recorded in db")
	}

	// Positional JSON object → multiple inputs, non-strings re-marshalled.
	captureStdout(t, func() { (&SubmitCmd{Capability: "cap", Task: `{"a":"x","n":{"k":1}}`, NoWait: true}).Run(a) })
	tm = next()
	if tm.Inputs["a"] != "x" || tm.Inputs["n"] != `{"k":1}` {
		t.Errorf("json submit: %+v", tm.Inputs)
	}

	// --input flags win over positional; --file merges.
	file := filepath.Join(t.TempDir(), "in.json")
	os.WriteFile(file, []byte(`{"f":"fv","num":2}`), 0o644)
	captureStdout(t, func() {
		(&SubmitCmd{Capability: "cap", Inputs: []string{"k=v", "k2=a=b"}, File: file, Task: "ignored", NoWait: true}).Run(a)
	})
	tm = next()
	if tm.Inputs["k"] != "v" || tm.Inputs["k2"] != "a=b" || tm.Inputs["f"] != "fv" || tm.Inputs["num"] != "2" || tm.Inputs["task"] != "" {
		t.Errorf("flag submit: %+v", tm.Inputs)
	}

	// Errors.
	if err := (&SubmitCmd{Capability: "cap", Inputs: []string{"novalue"}}).Run(a); err == nil || !strings.Contains(err.Error(), "invalid input format") {
		t.Errorf("bad input: %v", err)
	}
	if err := (&SubmitCmd{Capability: "cap", File: "/nonexistent"}).Run(a); err == nil || !strings.Contains(err.Error(), "read file") {
		t.Errorf("bad file: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if err := (&SubmitCmd{Capability: "cap", File: bad}).Run(a); err == nil || !strings.Contains(err.Error(), "parse json") {
		t.Errorf("bad json file: %v", err)
	}
	if err := (&SubmitCmd{Capability: "cap"}).Run(a); err == nil || !strings.Contains(err.Error(), "no inputs") {
		t.Errorf("no inputs: %v", err)
	}

	// Default (wait) path: a fake worker answers.
	// NOTE: the worker must answer after SubmitCmd has reached waitForResult;
	// an instant reply is dropped because waitForResult opens its own
	// subscription (parked smell, see record).
	go func() {
		msg, err := work.NextMsg(2 * time.Second)
		if err != nil {
			return
		}
		tm, _ := swarm.UnmarshalTaskMessage(msg.Data)
		time.Sleep(300 * time.Millisecond)
		res := swarm.NewTaskResult(tm.TaskID, "w1", swarm.ResultSuccess)
		res.Outputs = "done"
		data, _ := res.Marshal()
		nc.Publish("done.cap."+tm.TaskID, data)
	}()
	out = captureStdout(t, func() {
		if err := (&SubmitCmd{Capability: "cap", Task: "wait for me"}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, `"status": "success"`) || !strings.Contains(out, `"outputs": "done"`) {
		t.Errorf("wait submit output:\n%s", out)
	}
}

func TestDiscussCmd(t *testing.T) {
	nc, url := startNATS(t)
	a := newTestApp(t, url)
	sub, _ := nc.SubscribeSync("discuss.>")
	nc.Flush()

	out := captureStdout(t, func() {
		if err := (&DiscussCmd{Task: "topic"}).Run(a); err != nil {
			t.Error(err)
		}
	})
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tm, _ := swarm.UnmarshalTaskMessage(msg.Data)
	if tm.Inputs["task"] != "topic" || !strings.HasPrefix(msg.Subject, "discuss.t-") || strings.TrimSpace(out) != tm.TaskID {
		t.Errorf("discuss: %s %+v out=%q", msg.Subject, tm, out)
	}

	file := filepath.Join(t.TempDir(), "in.json")
	os.WriteFile(file, []byte(`{"q":"why","n":[1]}`), 0o644)
	captureStdout(t, func() { (&DiscussCmd{Inputs: []string{"a=b"}, File: file, Task: `{"z":"ignored"}`}).Run(a) })
	msg, _ = sub.NextMsg(2 * time.Second)
	tm, _ = swarm.UnmarshalTaskMessage(msg.Data)
	if tm.Inputs["a"] != "b" || tm.Inputs["q"] != "why" || tm.Inputs["n"] != "[1]" || tm.Inputs["z"] != "" {
		t.Errorf("discuss inputs: %+v", tm.Inputs)
	}
	captureStdout(t, func() { (&DiscussCmd{Task: `{"s":"str","o":{"k":true}}`}).Run(a) })
	msg, _ = sub.NextMsg(2 * time.Second)
	tm, _ = swarm.UnmarshalTaskMessage(msg.Data)
	if tm.Inputs["s"] != "str" || tm.Inputs["o"] != `{"k":true}` {
		t.Errorf("discuss json task: %+v", tm.Inputs)
	}

	if err := (&DiscussCmd{Inputs: []string{"bad"}}).Run(a); err == nil {
		t.Error("bad input should fail")
	}
	if err := (&DiscussCmd{File: "/nonexistent"}).Run(a); err == nil {
		t.Error("bad file should fail")
	}
	os.WriteFile(file, []byte("{"), 0o644)
	if err := (&DiscussCmd{File: file}).Run(a); err == nil {
		t.Error("bad json should fail")
	}
	if err := (&DiscussCmd{}).Run(a); err == nil || !strings.Contains(err.Error(), "no inputs") {
		t.Errorf("no inputs: %v", err)
	}
}

func TestResultCmd(t *testing.T) {
	nc, url := startNATS(t)
	a := newTestApp(t, url)
	db, _ := a.db()
	db.InsertTask(swarm.NewTaskMessage("t-1", "cap", nil), "pending")
	db.UpdateResult(swarm.NewTaskResult("t-1", "w1", swarm.ResultSuccess))

	out := captureStdout(t, func() {
		if err := (&ResultCmd{TaskID: "t-1"}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, `"task_id": "t-1"`) {
		t.Errorf("result output:\n%s", out)
	}
	if err := (&ResultCmd{TaskID: "t-2"}).Run(a); err == nil || !strings.Contains(err.Error(), "use --wait") {
		t.Errorf("not found: %v", err)
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		data, _ := swarm.NewTaskResult("t-2", "w1", swarm.ResultFailed).Marshal()
		nc.Publish("done.cap.t-2", data)
	}()
	out = captureStdout(t, func() {
		if err := (&ResultCmd{TaskID: "t-2", Wait: true}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, `"status": "failed"`) {
		t.Errorf("wait output:\n%s", out)
	}
}

func TestPurgeCmd(t *testing.T) {
	nc, url := startNATS(t)
	a := newTestApp(t, url)

	out := captureStdout(t, func() {
		if err := (&PurgeCmd{Force: true}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "nothing to purge") {
		t.Errorf("no stream:\n%s", out)
	}

	js, err := swarm.EnsureStream(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish("work.cap.t-1", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if err := (&PurgeCmd{Force: true}).Run(a); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Purged 1 messages") {
		t.Errorf("purge:\n%s", out)
	}
	info, _ := js.StreamInfo(swarm.StreamName)
	if info.State.Msgs != 0 {
		t.Errorf("stream not purged: %d", info.State.Msgs)
	}
}

func TestAppDBCreatesDataDir(t *testing.T) {
	a := newTestApp(t, "")
	db, err := a.db()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.dataDir); err != nil {
		t.Errorf("data dir not created: %v", err)
	}
	if db.dbPath != filepath.Join(a.dataDir, "swarm.db") {
		t.Errorf("db path: %s", db.dbPath)
	}
}
