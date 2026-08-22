// Copied VERBATIM from agentkit v0.2.1 (heartbeat/heartbeat.go + heartbeat/sender.go)
// to keep the swarm wire format stable: agentkit v1 dropped the heartbeat
// package and swarmkit's heartbeat renames the JSON field agent_id->agent.
// The only port is the bus type: the old bus.MessageBus became swarmkit's
// messaging.Bus; subjects ("heartbeat.<agent_id>") and payload are unchanged.
// The unused Monitor/MonitorConfig/MemorySender pieces of the original were
// not copied.

package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vinayprograms/swarmkit/messaging"
)

// Common errors.
var (
	ErrAlreadyStarted = errors.New("heartbeat already started")
	ErrNotStarted     = errors.New("heartbeat not started")
	ErrInvalidConfig  = errors.New("invalid configuration")
)

// SubjectPrefix is the subject prefix for heartbeat messages.
const SubjectPrefix = "heartbeat."

// Heartbeat represents a single heartbeat message from an agent.
type Heartbeat struct {
	// AgentID uniquely identifies the sending agent.
	AgentID string `json:"agent_id"`

	// Timestamp when the heartbeat was generated.
	Timestamp time.Time `json:"timestamp"`

	// Status of the agent (e.g., "idle", "busy", "draining").
	Status string `json:"status"`

	// Load is a normalized load metric (0.0 to 1.0).
	Load float64 `json:"load"`

	// Metadata contains additional key-value pairs.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Marshal serializes a heartbeat to JSON.
func (h *Heartbeat) Marshal() ([]byte, error) {
	return json.Marshal(h)
}

// UnmarshalHeartbeat deserializes a heartbeat from JSON.
// (Was heartbeat.Unmarshal in agentkit; renamed only because it now shares a
// package with the task envelopes.)
func UnmarshalHeartbeat(data []byte) (*Heartbeat, error) {
	var h Heartbeat
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Subject returns the subject for this heartbeat.
func (h *Heartbeat) Subject() string {
	return SubjectPrefix + h.AgentID
}

// SenderConfig configures a heartbeat sender.
type SenderConfig struct {
	// Bus is the message bus for publishing heartbeats.
	Bus messaging.Bus

	// AgentID is the unique identifier for this agent.
	AgentID string

	// Interval between heartbeats.
	// Default: 5 seconds
	Interval time.Duration

	// InitialStatus is the starting status.
	// Default: "idle"
	InitialStatus string

	// Logger receives a warning for every heartbeat that fails to publish.
	// Default: slog.Default()
	Logger *slog.Logger
}

// Validate checks the configuration.
func (c *SenderConfig) Validate() error {
	if c.Bus == nil {
		return ErrInvalidConfig
	}
	if c.AgentID == "" {
		return ErrInvalidConfig
	}
	return nil
}

// DefaultSenderConfig returns configuration with sensible defaults.
func DefaultSenderConfig() SenderConfig {
	return SenderConfig{
		Interval:      5 * time.Second,
		InitialStatus: "idle",
	}
}

// BusSender sends heartbeats over a message bus.
type BusSender struct {
	bus      messaging.Bus
	agentID  string
	interval time.Duration
	logger   *slog.Logger

	mu       sync.RWMutex
	status   string
	load     float64
	metadata map[string]string

	running atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewBusSender creates a new heartbeat sender.
func NewBusSender(cfg SenderConfig) (*BusSender, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultSenderConfig().Interval
	}

	status := cfg.InitialStatus
	if status == "" {
		status = DefaultSenderConfig().InitialStatus
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &BusSender{
		bus:      cfg.Bus,
		agentID:  cfg.AgentID,
		interval: interval,
		logger:   logger.With("component", "heartbeat", "agent_id", cfg.AgentID),
		status:   status,
		metadata: make(map[string]string),
	}, nil
}

// Start begins sending heartbeats at the configured interval.
func (s *BusSender) Start(ctx context.Context) error {
	if s.running.Swap(true) {
		return ErrAlreadyStarted
	}

	if ctx == nil {
		ctx = context.Background()
	}

	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})

	go s.run(ctx)
	return nil
}

// run is the main heartbeat loop: one beat immediately, then one per
// interval. A failed publish is logged and retried on the next tick.
func (s *BusSender) run(ctx context.Context) {
	defer close(s.doneCh)

	s.beat()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.running.Store(false)
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.beat()
		}
	}
}

// beat publishes one heartbeat, logging a failure instead of returning it.
func (s *BusSender) beat() {
	if err := s.sendHeartbeat(); err != nil {
		s.logger.Warn("heartbeat publish failed", "error", err.Error())
	}
}

// sendHeartbeat publishes a heartbeat message.
func (s *BusSender) sendHeartbeat() error {
	hb := s.buildHeartbeat()
	data, err := hb.Marshal()
	if err != nil {
		return err
	}
	return s.bus.Publish(hb.Subject(), data)
}

// buildHeartbeat creates a heartbeat with current state.
func (s *BusSender) buildHeartbeat() *Heartbeat {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hb := &Heartbeat{
		AgentID:   s.agentID,
		Timestamp: time.Now(),
		Status:    s.status,
		Load:      s.load,
	}

	if len(s.metadata) > 0 {
		hb.Metadata = maps.Clone(s.metadata)
	}

	return hb
}

// SetStatus updates the status included in heartbeats.
func (s *BusSender) SetStatus(status string) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

// SetLoad updates the load metric.
func (s *BusSender) SetLoad(load float64) {
	s.mu.Lock()
	if load < 0 {
		load = 0
	}
	if load > 1 {
		load = 1
	}
	s.load = load
	s.mu.Unlock()
}

// SetMetadata updates a metadata field.
func (s *BusSender) SetMetadata(key, value string) {
	s.mu.Lock()
	s.metadata[key] = value
	s.mu.Unlock()
}

// Stop stops sending heartbeats.
func (s *BusSender) Stop() error {
	if !s.running.Swap(false) {
		return ErrNotStarted
	}
	close(s.stopCh)
	<-s.doneCh
	return nil
}

// AgentID returns the sender's agent ID.
func (s *BusSender) AgentID() string {
	return s.agentID
}
