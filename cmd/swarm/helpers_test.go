package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startNATS runs an embedded NATS server (with JetStream, for PurgeCmd) and
// returns a client connection plus the server URL.
func startNATS(t *testing.T) (*nats.Conn, string) {
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
	return nc, srv.ClientURL()
}

// newTestDB returns a taskDB rooted in a fresh temp dir.
func newTestDB(t *testing.T) (*taskDB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := openTaskDB(filepath.Join(dir, "swarm.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db, dir
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout = old
	w.Close()
	return <-done
}

// newTestApp returns an app pointed at the given NATS URL with a temp data dir.
func newTestApp(t *testing.T, natsURL string) *app {
	t.Helper()
	dir := t.TempDir()
	return &app{natsURL: natsURL, configDir: filepath.Join(dir, "config"), dataDir: filepath.Join(dir, "data")}
}
