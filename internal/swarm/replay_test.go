package swarm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vinayprograms/agentkit/heartbeat"
	"github.com/vinayprograms/agentkit/tasks"
	"github.com/vinayprograms/agent/internal/executor"
)

func TestProcessReplayHeartbeat(t *testing.T) {
	sc := executor.NewSwarmContext()

	hb := &heartbeat.Heartbeat{
		AgentID:   "backend",
		Timestamp: time.Now(),
		Status:    "executing",
		Metadata: map[string]string{
			"capability":   "golang",
			"current_task": "task-123",
		},
	}
	data, _ := hb.Marshal()

	processReplayHeartbeat(sc, data)

	states := sc.GetAgentStates()
	if len(states) != 1 {
		t.Fatalf("Expected 1 agent, got %d", len(states))
	}
	if states[0].Status != "executing" {
		t.Errorf("Expected executing, got %s", states[0].Status)
	}
	if states[0].Capability != "golang" {
		t.Errorf("Expected golang, got %s", states[0].Capability)
	}
}

func TestProcessReplayDiscuss(t *testing.T) {
	sc := executor.NewSwarmContext()

	result := tasks.TaskResult{
		TaskID:      "task-123",
		AgentID:     "backend",
		Outputs:     "CLAIM: I'll handle the API",
		CompletedAt: time.Now(),
		Metadata:    map[string]string{"signal": "CLAIM"},
	}
	data, _ := json.Marshal(result)

	processReplayDiscuss(sc, "discuss.task-123", data, "frontend")

	msgs := sc.GetDiscussion("task-123")
	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Signal != "CLAIM" {
		t.Errorf("Expected CLAIM, got %s", msgs[0].Signal)
	}
}

func TestProcessReplayDiscuss_SkipsSelf(t *testing.T) {
	sc := executor.NewSwarmContext()

	result := tasks.TaskResult{
		TaskID:  "task-123",
		AgentID: "backend",
		Outputs: "my own message",
	}
	data, _ := json.Marshal(result)

	// When selfID matches, message should be skipped
	processReplayDiscuss(sc, "discuss.task-123", data, "backend")

	msgs := sc.GetDiscussion("task-123")
	if len(msgs) != 0 {
		t.Errorf("Expected 0 messages (self filtered), got %d", len(msgs))
	}
}

func TestProcessReplayDone(t *testing.T) {
	sc := executor.NewSwarmContext()

	result := tasks.TaskResult{
		TaskID:      "task-123",
		AgentID:     "backend",
		Outputs:     "API complete",
		CompletedAt: time.Now(),
	}
	data, _ := json.Marshal(result)

	processReplayDone(sc, "done.golang.task-123", data)

	completed := sc.GetCompleted()
	if len(completed) != 1 {
		t.Fatalf("Expected 1 completed, got %d", len(completed))
	}
	if completed["task-123"].Signal != "DONE" {
		t.Errorf("Expected DONE signal, got %s", completed["task-123"].Signal)
	}
}

func TestProcessReplayMessage_Routing(t *testing.T) {
	sc := executor.NewSwarmContext()

	// Heartbeat
	hb := &heartbeat.Heartbeat{AgentID: "agent1", Status: "monitoring", Timestamp: time.Now()}
	hbData, _ := hb.Marshal()
	processReplayMessage(sc, "heartbeat.agent1", hbData, "self")

	// Discuss
	result := tasks.TaskResult{AgentID: "agent2", TaskID: "t1", Outputs: "test", CompletedAt: time.Now()}
	discData, _ := json.Marshal(result)
	processReplayMessage(sc, "discuss.t1", discData, "self")

	// Done
	doneResult := tasks.TaskResult{AgentID: "agent3", TaskID: "t2", Outputs: "done", CompletedAt: time.Now()}
	doneData, _ := json.Marshal(doneResult)
	processReplayMessage(sc, "done.cap.t2", doneData, "self")

	// Work messages are ignored
	processReplayMessage(sc, "work.golang.t3", []byte("{}"), "self")

	states := sc.GetAgentStates()
	if len(states) != 1 {
		t.Errorf("Expected 1 agent from heartbeat, got %d", len(states))
	}

	msgs := sc.GetDiscussion("t1")
	if len(msgs) != 1 {
		t.Errorf("Expected 1 discuss message, got %d", len(msgs))
	}

	completed := sc.GetCompleted()
	if len(completed) != 1 {
		t.Errorf("Expected 1 completed, got %d", len(completed))
	}
}
