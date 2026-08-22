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
