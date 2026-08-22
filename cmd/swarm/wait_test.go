package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/swarm"
)

func TestWaitForResultReceivesAndPersists(t *testing.T) {
	nc, _ := startNATS(t)
	db, _ := newTestDB(t)
	db.InsertTask(swarm.NewTaskMessage("t-1", "cap", nil), "pending")

	go func() {
		// Give waitForResult time to subscribe, then emit heartbeat + result.
		time.Sleep(200 * time.Millisecond)
		hb, _ := (&swarm.Heartbeat{AgentID: "w1", Status: "busy"}).Marshal()
		nc.Publish("heartbeat.w1", hb)
		res := swarm.NewTaskResult("t-1", "w1", swarm.ResultSuccess)
		res.DurationMs = 5
		data, _ := res.Marshal()
		nc.Publish("done.cap.t-1", data)
	}()

	got, err := waitForResult(nc, "t-1", db)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "t-1" || got.AgentID != "w1" || got.DurationMs != 5 {
		t.Errorf("result: %+v", got)
	}
	saved, err := db.GetResult("t-1")
	if err != nil || saved.Status != swarm.ResultSuccess {
		t.Errorf("result not persisted: %v %+v", err, saved)
	}
	recs, _ := db.ListTasks("", "", 10)
	if recs[0].Status != "success" {
		t.Errorf("record status: %+v", recs)
	}
}

func TestWaitForResultNilDBAndBadPayload(t *testing.T) {
	nc, _ := startNATS(t)
	go func() {
		time.Sleep(200 * time.Millisecond)
		nc.Publish("done.cap.t-2", []byte("{not json"))
	}()
	_, err := waitForResult(nc, "t-2", nil)
	if err == nil || !strings.Contains(err.Error(), "parse result") {
		t.Errorf("want parse error, got %v", err)
	}

	go func() {
		time.Sleep(200 * time.Millisecond)
		data, _ := swarm.NewTaskResult("t-3", "w", swarm.ResultTimeout).Marshal()
		nc.Publish("done.cap.t-3", data)
	}()
	got, err := waitForResult(nc, "t-3", nil)
	if err != nil || got.Status != swarm.ResultTimeout {
		t.Errorf("nil db: %v %+v", err, got)
	}
	// The "all agents dead" branch needs 30s (heartbeatTimeout const) — not exercised.
}

func TestWaitForResultClosedConn(t *testing.T) {
	nc, _ := startNATS(t)
	nc.Close()
	if _, err := waitForResult(nc, "t", nil); err == nil || !strings.Contains(err.Error(), "subscribe result") {
		t.Errorf("closed conn: %v", err)
	}
}
