package swarm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agentkit/heartbeat"
	"github.com/vinayprograms/agentkit/tasks"
	"github.com/vinayprograms/agent/internal/executor"
)

// ReplayResult holds the output of the REPLAY phase.
type ReplayResult struct {
	SwarmContext *executor.SwarmContext
	MessagesRead uint64
	CatchupSeq   uint64
}

// Replay consumes all JetStream messages up to catchupSeq, building the
// swarm context. This is the REPLAY phase — the agent's initialization
// before entering the collaboration state machine.
//
// Messages are consumed silently (context-building only). No deliberation
// is triggered for replayed messages.
func Replay(js nats.JetStreamContext, catchupSeq uint64, agentID string) (*ReplayResult, error) {
	if catchupSeq == 0 {
		// Empty stream — nothing to replay
		return &ReplayResult{
			SwarmContext: executor.NewSwarmContext(),
			CatchupSeq:  0,
		}, nil
	}

	sc := executor.NewSwarmContext()
	var messagesRead uint64

	// Create an ephemeral consumer starting from the beginning
	sub, err := js.SubscribeSync(
		">", // All subjects in the stream
		nats.DeliverAll(),
		nats.AckNone(), // Read-only replay, no ack needed
	)
	if err != nil {
		return nil, fmt.Errorf("replay subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	// Consume messages until we reach catchupSeq
	for {
		msg, err := sub.NextMsg(5 * time.Second)
		if err != nil {
			// Timeout or error — we've consumed what's available
			break
		}

		meta, err := msg.Metadata()
		if err != nil {
			continue
		}

		messagesRead++
		processReplayMessage(sc, msg.Subject, msg.Data, agentID)

		if meta.Sequence.Stream >= catchupSeq {
			break // Caught up
		}
	}

	return &ReplayResult{
		SwarmContext:  sc,
		MessagesRead: messagesRead,
		CatchupSeq:   catchupSeq,
	}, nil
}

// processReplayMessage routes a replayed message to the appropriate
// swarm context handler based on the subject prefix.
func processReplayMessage(sc *executor.SwarmContext, subject string, data []byte, selfID string) {
	switch {
	case strings.HasPrefix(subject, "heartbeat."):
		processReplayHeartbeat(sc, data)

	case strings.HasPrefix(subject, "discuss."):
		processReplayDiscuss(sc, subject, data, selfID)

	case strings.HasPrefix(subject, "done."):
		processReplayDone(sc, subject, data)
	}
	// work.* messages are not replayed into swarm context — they are
	// directed assignments consumed by the agent's normal work subscription.
}

// processReplayHeartbeat updates agent state from a heartbeat message.
func processReplayHeartbeat(sc *executor.SwarmContext, data []byte) {
	hb, err := heartbeat.Unmarshal(data)
	if err != nil {
		return
	}
	capability := ""
	currentTask := ""
	if hb.Metadata != nil {
		capability = hb.Metadata["capability"]
		currentTask = hb.Metadata["current_task"]
	}
	sc.UpdateAgent(hb.AgentID, hb.Status, capability, currentTask, hb.Timestamp)
}

// processReplayDiscuss records a discuss message in the task discussion log.
func processReplayDiscuss(sc *executor.SwarmContext, subject string, data []byte, selfID string) {
	// Extract task ID from subject: discuss.<task_id>
	parts := strings.SplitN(subject, ".", 2)
	if len(parts) < 2 {
		return
	}
	taskID := parts[1]

	// Try to parse as TaskResult (structured discuss message)
	var result tasks.TaskResult
	if err := json.Unmarshal(data, &result); err == nil && result.AgentID != "" {
		// Skip own messages
		if result.AgentID == selfID {
			return
		}
		signal := ""
		if result.Metadata != nil {
			signal = result.Metadata["signal"]
		}
		content := fmt.Sprintf("%v", result.Outputs)
		sc.AddDiscussMessage(taskID, executor.DiscussMessage{
			From:      result.AgentID,
			Timestamp: result.CompletedAt,
			Content:   content,
			Signal:    signal,
		})
		return
	}

	// Try to parse as TaskMessage (initial task submission)
	task, err := tasks.UnmarshalTaskMessage(data)
	if err != nil {
		return
	}
	if task.SubmittedBy == selfID {
		return
	}
	sc.AddDiscussMessage(taskID, executor.DiscussMessage{
		From:      task.SubmittedBy,
		Timestamp: time.Now(), // TaskMessage doesn't have timestamp
		Content:   task.Inputs["goal"],
	})
}

// processReplayDone records a completion in swarm context.
func processReplayDone(sc *executor.SwarmContext, subject string, data []byte) {
	// Extract task ID from subject: done.<capability>.<task_id>
	parts := strings.SplitN(subject, ".", 3)
	if len(parts) < 3 {
		return
	}
	taskID := parts[2]

	var result tasks.TaskResult
	if err := json.Unmarshal(data, &result); err != nil {
		return
	}

	content := fmt.Sprintf("%v", result.Outputs)
	sc.AddDiscussMessage(taskID, executor.DiscussMessage{
		From:      result.AgentID,
		Timestamp: result.CompletedAt,
		Content:   content,
		Signal:    "DONE",
	})
}
