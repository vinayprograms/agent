package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agentkit/tasks"
)

// heartbeatTimeout is how long to wait after last heartbeat before declaring agents dead.
const heartbeatTimeout = 30 * time.Second

// waitForResult waits for a task result while monitoring agent heartbeats.
// It only times out if ALL agents stop sending heartbeats (i.e., they're dead).
// Returns the result when received, or error if all agents are gone.
func waitForResult(nc *nats.Conn, taskID string, db *taskDB) (*tasks.TaskResult, error) {
	// Subscribe to result
	resultSub, err := nc.SubscribeSync(fmt.Sprintf("done.*.%s", taskID))
	if err != nil {
		return nil, fmt.Errorf("subscribe result: %w", err)
	}
	defer resultSub.Unsubscribe()

	// Subscribe to heartbeats
	hbSub, err := nc.SubscribeSync("heartbeat.>")
	if err != nil {
		return nil, fmt.Errorf("subscribe heartbeat: %w", err)
	}
	defer hbSub.Unsubscribe()

	lastHeartbeat := time.Now()
	fmt.Fprintf(os.Stderr, "Waiting for result (monitoring agent heartbeats)...\n")

	for {
		// Check for result (non-blocking, short timeout)
		msg, err := resultSub.NextMsg(1 * time.Second)
		if err == nil {
			// Got result!
			var result tasks.TaskResult
			if err := json.Unmarshal(msg.Data, &result); err != nil {
				return nil, fmt.Errorf("parse result: %w", err)
			}

			// Save to DB
			if db != nil {
				if err := db.UpdateResult(&result); err != nil {
					fmt.Fprintf(os.Stderr, "⚠️  Failed to save result: %v\n", err)
				}
			}

			return &result, nil
		}

		// Drain heartbeat messages to track liveness
		for {
			hbMsg, err := hbSub.NextMsg(1 * time.Millisecond)
			if err != nil {
				break // No more heartbeats in queue
			}
			_ = hbMsg
			lastHeartbeat = time.Now()
		}

		// Check if agents are still alive
		sinceLastHB := time.Since(lastHeartbeat)
		if sinceLastHB > heartbeatTimeout {
			return nil, fmt.Errorf("no agent heartbeats for %s — agents may be down", sinceLastHB.Round(time.Second))
		}
	}
}
