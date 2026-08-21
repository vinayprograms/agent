package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startJetStream runs an embedded NATS server with JetStream and returns a connection to it.
func startJetStream(t *testing.T) *nats.Conn {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server did not start")
	}
	t.Cleanup(srv.Shutdown)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestEnsureStream(t *testing.T) {
	nc := startJetStream(t)

	js, err := EnsureStream(nc)
	if err != nil {
		t.Fatal(err)
	}
	info, err := js.StreamInfo(StreamName)
	if err != nil {
		t.Fatal(err)
	}
	cfg := info.Config
	if strings.Join(cfg.Subjects, ",") != "discuss.>,work.>,done.>,heartbeat.>" ||
		cfg.Retention != nats.LimitsPolicy || cfg.MaxAge != 24*time.Hour || cfg.Storage != nats.MemoryStorage {
		t.Errorf("stream config = %+v", cfg)
	}

	// Second call binds to the existing stream.
	if _, err := EnsureStream(nc); err != nil {
		t.Fatalf("EnsureStream on existing stream: %v", err)
	}
}

func TestEnsureStreamSubjectOverlap(t *testing.T) {
	nc := startJetStream(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{Name: "OTHER", Subjects: []string{"work.>"}}); err != nil {
		t.Fatal(err)
	}
	_, err = EnsureStream(nc)
	if err == nil || !strings.Contains(err.Error(), "create stream SWARM") {
		t.Errorf("err = %v, want create stream failure", err)
	}
}

func TestEnsureWorkConsumer(t *testing.T) {
	nc := startJetStream(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}

	// Without the stream the pull subscribe must fail.
	if _, err := EnsureWorkConsumer(js, "develop"); err == nil || !strings.Contains(err.Error(), "pull subscribe work.develop.*") {
		t.Errorf("err = %v, want pull subscribe failure", err)
	}

	js, err = EnsureStream(nc)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := EnsureWorkConsumer(js, "develop")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ci, err := js.ConsumerInfo(StreamName, "work-develop")
	if err != nil {
		t.Fatal(err)
	}
	c := ci.Config
	if c.FilterSubject != "work.develop.*" || c.AckPolicy != nats.AckExplicitPolicy || c.MaxDeliver != 3 ||
		c.AckWait != 10*time.Minute || c.DeliverPolicy != nats.DeliverNewPolicy {
		t.Errorf("consumer config = %+v", c)
	}

	if _, err := js.Publish("work.develop.t1", []byte(`{"task_id":"t1"}`)); err != nil {
		t.Fatal(err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil || len(msgs) != 1 || msgs[0].Subject != "work.develop.t1" {
		t.Fatalf("Fetch = %v, %v", msgs, err)
	}
}

func TestLastSequence(t *testing.T) {
	nc := startJetStream(t)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LastSequence(js); err == nil || !strings.Contains(err.Error(), "stream info") {
		t.Errorf("err = %v, want stream info failure", err)
	}

	js, err = EnsureStream(nc)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := LastSequence(js)
	if err != nil || seq != 0 {
		t.Fatalf("LastSequence(empty) = %d, %v", seq, err)
	}
	for range 3 {
		if _, err := js.Publish("heartbeat.a", []byte(`{"agent_id":"a"}`)); err != nil {
			t.Fatal(err)
		}
	}
	seq, err = LastSequence(js)
	if err != nil || seq != 3 {
		t.Fatalf("LastSequence = %d, %v, want 3", seq, err)
	}
}

func TestReplay(t *testing.T) {
	nc := startJetStream(t)

	t.Run("empty stream", func(t *testing.T) {
		js, err := nc.JetStream()
		if err != nil {
			t.Fatal(err)
		}
		res, err := Replay(js, 0, "self")
		if err != nil || res.MessagesRead != 0 || res.CatchupSeq != 0 || res.SwarmContext == nil {
			t.Fatalf("Replay(0) = %+v, %v", res, err)
		}
		// No stream yet: subscribing fails.
		if _, err := Replay(js, 1, "self"); err == nil || !strings.Contains(err.Error(), "replay subscribe") {
			t.Errorf("err = %v, want replay subscribe failure", err)
		}
	})

	js, err := EnsureStream(nc)
	if err != nil {
		t.Fatal(err)
	}
	hb := &Heartbeat{AgentID: "a1", Status: "executing", Timestamp: time.Now(), Metadata: map[string]string{"capability": "golang"}}
	hbData, _ := hb.Marshal()
	done := &TaskResult{TaskID: "t1", AgentID: "a1", Outputs: "ok", CompletedAt: time.Now()}
	doneData, _ := done.Marshal()
	for _, m := range []struct {
		subj string
		data []byte
	}{
		{"heartbeat.a1", hbData},
		{"discuss.t1", doneData},
		{"done.golang.t1", doneData},
		{"work.golang.t2", []byte("{}")},
	} {
		if _, err := js.Publish(m.subj, m.data); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("catch up to sequence", func(t *testing.T) {
		res, err := Replay(js, 2, "self")
		if err != nil {
			t.Fatal(err)
		}
		if res.MessagesRead != 2 || res.CatchupSeq != 2 {
			t.Errorf("result = %+v", res)
		}
		if states := res.SwarmContext.GetAgentStates(); len(states) != 1 || states[0].Capability != "golang" {
			t.Errorf("agent states = %+v", states)
		}
		if len(res.SwarmContext.GetDiscussion("t1")) != 1 {
			t.Error("expected 1 discuss message")
		}
		if len(res.SwarmContext.GetCompleted()) != 0 {
			t.Error("done message beyond catchup must not be replayed")
		}
	})

	t.Run("catchup beyond stream end drains and times out", func(t *testing.T) {
		res, err := Replay(js, 10, "self")
		if err != nil {
			t.Fatal(err)
		}
		if res.MessagesRead != 4 || res.CatchupSeq != 10 {
			t.Errorf("result = %+v", res)
		}
		if len(res.SwarmContext.GetCompleted()) != 1 {
			t.Error("expected done message replayed")
		}
	})
}
