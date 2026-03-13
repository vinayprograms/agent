package swarm

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// StreamName is the single JetStream stream covering all swarm subjects.
const StreamName = "SWARM"

// EnsureStream creates the JetStream stream for the swarm if it doesn't exist.
// All subject families are covered by a single stream — there is no reason
// to selectively apply durability.
func EnsureStream(nc *nats.Conn) (nats.JetStreamContext, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("jetstream context: %w", err)
	}

	// Check if stream already exists
	_, err = js.StreamInfo(StreamName)
	if err == nil {
		return js, nil // Already exists
	}

	// Create the stream covering all subject families.
	// LimitsPolicy retains messages until MaxAge, independent of consumer state.
	// This supports both replay (catch-up) and durable pull consumers for work distribution.
	_, err = js.AddStream(&nats.StreamConfig{
		Name: StreamName,
		Subjects: []string{
			"discuss.>",
			"work.>",
			"done.>",
			"heartbeat.>",
		},
		Retention:  nats.LimitsPolicy,  // Keep messages until MaxAge
		MaxAge:     24 * time.Hour,     // Swarm lifetime (generous — cleaned by `swarm purge`)
		Storage:    nats.MemoryStorage, // Ephemeral — doesn't persist across server restarts
		Duplicates: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream %s: %w", StreamName, err)
	}

	return js, nil
}

// EnsureWorkConsumer creates a durable pull consumer for a capability's work queue.
// Each capability gets one consumer shared by all workers via pull-based delivery.
// Workers call Fetch() to pull tasks — NATS tracks ack state per consumer,
// guaranteeing each message is delivered to exactly one worker.
func EnsureWorkConsumer(js nats.JetStreamContext, capability string) (*nats.Subscription, error) {
	consumerName := fmt.Sprintf("work-%s", capability)
	filterSubject := fmt.Sprintf("work.%s.*", capability)

	// Create or bind to existing durable pull consumer
	sub, err := js.PullSubscribe(
		filterSubject,
		consumerName,
		nats.BindStream(StreamName),
		nats.AckExplicit(),                   // Worker must ack after processing
		nats.MaxDeliver(3),                   // Retry up to 3 times on nack/timeout
		nats.AckWait(10*time.Minute),         // Long timeout — tasks can take minutes
		nats.DeliverNew(),                    // Only new messages (not replayed history)
	)
	if err != nil {
		return nil, fmt.Errorf("pull subscribe work.%s.*: %w", capability, err)
	}

	return sub, nil
}

// LastSequence returns the current last sequence number of the swarm stream.
// Used by the REPLAY phase to determine the catch-up boundary.
func LastSequence(js nats.JetStreamContext) (uint64, error) {
	info, err := js.StreamInfo(StreamName)
	if err != nil {
		return 0, fmt.Errorf("stream info: %w", err)
	}
	return info.State.LastSeq, nil
}
