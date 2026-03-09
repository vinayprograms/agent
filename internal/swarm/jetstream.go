package swarm

import (
	"fmt"

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

	// Create the stream covering all subject families
	_, err = js.AddStream(&nats.StreamConfig{
		Name: StreamName,
		Subjects: []string{
			"discuss.>",
			"work.>",
			"done.>",
			"heartbeat.>",
		},
		Retention:  nats.InterestPolicy, // Messages retained while consumers exist
		MaxAge:     0,                    // No age limit (swarm lifetime)
		Storage:    nats.MemoryStorage,   // Ephemeral — swarm context doesn't persist across restarts
		Duplicates: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream %s: %w", StreamName, err)
	}

	return js, nil
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
