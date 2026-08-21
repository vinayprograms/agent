package swarm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatMarshalRoundTrip(t *testing.T) {
	hb := &Heartbeat{AgentID: "a1", Timestamp: time.Now(), Status: "idle", Load: 0.5}
	if hb.Subject() != "heartbeat.a1" {
		t.Errorf("Subject() = %q", hb.Subject())
	}
	data, err := hb.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalHeartbeat(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "a1" || got.Status != "idle" || got.Load != 0.5 {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if _, err := UnmarshalHeartbeat([]byte("nope")); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestSenderConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  SenderConfig
		err  error
	}{
		{"nil bus", SenderConfig{AgentID: "a"}, ErrInvalidConfig},
		{"empty id", SenderConfig{Bus: &fakeBus{}}, ErrInvalidConfig},
		{"ok", SenderConfig{Bus: &fakeBus{}, AgentID: "a"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); !errors.Is(err, tt.err) {
				t.Fatalf("Validate() = %v, want %v", err, tt.err)
			}
		})
	}
	if _, err := NewBusSender(SenderConfig{}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("NewBusSender(invalid) = %v", err)
	}
	d := DefaultSenderConfig()
	if d.Interval != 5*time.Second || d.InitialStatus != "idle" {
		t.Errorf("DefaultSenderConfig = %+v", d)
	}
}

func TestBusSenderDefaults(t *testing.T) {
	s, err := NewBusSender(SenderConfig{Bus: &fakeBus{}, AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if s.interval != 5*time.Second || s.status != "idle" || s.AgentID() != "a" {
		t.Errorf("defaults not applied: interval=%v status=%q id=%q", s.interval, s.status, s.AgentID())
	}
}

func TestBusSenderLifecycle(t *testing.T) {
	bus := &fakeBus{}
	s, err := NewBusSender(SenderConfig{Bus: bus, AgentID: "a", Interval: 10 * time.Millisecond, InitialStatus: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Stop before Start = %v, want ErrNotStarted", err)
	}

	var calls atomic.Int32
	s.SetCallback(func() { calls.Add(1) })
	s.SetMetadata("k", "v")
	s.SetLoad(2)  // clamped to 1
	s.SetLoad(-1) // clamped to 0
	s.SetLoad(0.25)
	s.SetStatus("executing")

	if err := s.Start(nil); err != nil { //nolint:staticcheck // nil ctx is tolerated by design
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("second Start = %v, want ErrAlreadyStarted", err)
	}

	// Wait for the immediate beat plus at least one tick.
	deadline := time.Now().Add(2 * time.Second)
	for len(bus.messages()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}

	msgs := bus.messages()
	if len(msgs) < 2 {
		t.Fatalf("expected >=2 heartbeats, got %d", len(msgs))
	}
	if msgs[0].Subject != "heartbeat.a" {
		t.Errorf("subject = %q", msgs[0].Subject)
	}
	hb, err := UnmarshalHeartbeat(msgs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if hb.AgentID != "a" || hb.Status != "executing" || hb.Load != 0.25 || hb.Metadata["k"] != "v" {
		t.Errorf("heartbeat payload = %+v", hb)
	}
	if calls.Load() == 0 {
		t.Error("callback never invoked")
	}
}

func TestBusSenderContextCancel(t *testing.T) {
	s, err := NewBusSender(SenderConfig{Bus: &fakeBus{}, AgentID: "a", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-s.doneCh
	if s.running.Load() {
		t.Error("running should be false after ctx cancel")
	}
	if err := s.Stop(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Stop after ctx cancel = %v, want ErrNotStarted", err)
	}
}

func TestBusSenderPublishError(t *testing.T) {
	want := errors.New("boom")
	s, err := NewBusSender(SenderConfig{Bus: &fakeBus{err: want}, AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	var called bool
	s.SetCallback(func() { called = true })
	if err := s.sendHeartbeat(); !errors.Is(err, want) {
		t.Errorf("sendHeartbeat = %v, want %v", err, want)
	}
	if called {
		t.Error("callback must not run when publish fails")
	}

	// The run loop tolerates a failing initial beat.
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}
