package executor

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AgentState represents an agent's current state as observed from heartbeats.
type AgentState struct {
	AgentID    string
	Status     string // "replay", "monitoring", "deliberating", "executing"
	Capability string
	CurrentTask string
	LastSeen   time.Time
}

// DiscussMessage represents a single message in a task's discussion log.
type DiscussMessage struct {
	From      string
	Timestamp time.Time
	Content   string
	Signal    string // "CLAIM", "NEED_INFO", "DONE", or empty
}

// TaskDiscussion holds the discussion history for a single task.
type TaskDiscussion struct {
	TaskID   string
	Messages []DiscussMessage
}

// SwarmContext maintains the agent's personal, ephemeral view of the swarm.
// It is passively updated from NATS message handlers and read during
// deliberation and interrupt processing.
//
// Thread-safe — written by NATS handler goroutines, read by the executor.
type SwarmContext struct {
	mu sync.RWMutex

	// Agent states from heartbeats (agent_id → state)
	agents map[string]*AgentState

	// Task discussion logs (task_id → discussion)
	discussions map[string]*TaskDiscussion

	// Completed work (task_id → last DONE message)
	completed map[string]*DiscussMessage
}

// NewSwarmContext creates an empty swarm context.
func NewSwarmContext() *SwarmContext {
	return &SwarmContext{
		agents:      make(map[string]*AgentState),
		discussions: make(map[string]*TaskDiscussion),
		completed:   make(map[string]*DiscussMessage),
	}
}

// UpdateAgent updates an agent's state from a heartbeat.
func (sc *SwarmContext) UpdateAgent(agentID, status, capability, currentTask string, timestamp time.Time) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.agents[agentID] = &AgentState{
		AgentID:     agentID,
		Status:      status,
		Capability:  capability,
		CurrentTask: currentTask,
		LastSeen:    timestamp,
	}
}

// AddDiscussMessage records a discuss message for a task.
func (sc *SwarmContext) AddDiscussMessage(taskID string, msg DiscussMessage) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	disc, ok := sc.discussions[taskID]
	if !ok {
		disc = &TaskDiscussion{TaskID: taskID}
		sc.discussions[taskID] = disc
	}
	disc.Messages = append(disc.Messages, msg)

	// Track completions
	if msg.Signal == "DONE" {
		sc.completed[taskID] = &msg
	}
}

// GetAgentStates returns a snapshot of all agent states.
func (sc *SwarmContext) GetAgentStates() []AgentState {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	states := make([]AgentState, 0, len(sc.agents))
	for _, s := range sc.agents {
		states = append(states, *s)
	}
	return states
}

// GetDiscussion returns the discussion log for a task.
func (sc *SwarmContext) GetDiscussion(taskID string) []DiscussMessage {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	disc, ok := sc.discussions[taskID]
	if !ok {
		return nil
	}
	msgs := make([]DiscussMessage, len(disc.Messages))
	copy(msgs, disc.Messages)
	return msgs
}

// GetDiscussions returns all task IDs that have active discussions.
func (sc *SwarmContext) GetDiscussions() []string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	ids := make([]string, 0, len(sc.discussions))
	for id := range sc.discussions {
		ids = append(ids, id)
	}
	return ids
}

// GetCompleted returns completed task IDs and their DONE messages.
func (sc *SwarmContext) GetCompleted() map[string]DiscussMessage {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	result := make(map[string]DiscussMessage, len(sc.completed))
	for k, v := range sc.completed {
		result[k] = *v
	}
	return result
}

// FormatForLLM formats the swarm context as a compact text block
// for injection into LLM context during deliberation or interrupt processing.
func (sc *SwarmContext) FormatForLLM(relevantTaskID string) string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	var b strings.Builder

	// Agent states — always included
	b.WriteString("<swarm-context>\n")
	b.WriteString("  <agents>\n")
	for _, agent := range sc.agents {
		taskInfo := ""
		if agent.CurrentTask != "" {
			taskInfo = fmt.Sprintf(" task=%q", agent.CurrentTask)
		}
		b.WriteString(fmt.Sprintf("    <agent id=%q capability=%q status=%q%s last-seen=%q/>\n",
			agent.AgentID, agent.Capability, agent.Status, taskInfo,
			agent.LastSeen.Format(time.RFC3339)))
	}
	b.WriteString("  </agents>\n")

	// Relevant task discussion — if provided
	if relevantTaskID != "" {
		if disc, ok := sc.discussions[relevantTaskID]; ok && len(disc.Messages) > 0 {
			b.WriteString(fmt.Sprintf("\n  <discussion task=%q>\n", relevantTaskID))
			for _, msg := range disc.Messages {
				signal := ""
				if msg.Signal != "" {
					signal = fmt.Sprintf(" signal=%q", msg.Signal)
				}
				b.WriteString(fmt.Sprintf("    <message from=%q timestamp=%q%s>\n",
					msg.From, msg.Timestamp.Format(time.RFC3339), signal))
				b.WriteString(fmt.Sprintf("      %s\n", msg.Content))
				b.WriteString("    </message>\n")
			}
			b.WriteString("  </discussion>\n")
		}
	}

	// Completed work — compact
	if len(sc.completed) > 0 {
		b.WriteString("\n  <completed>\n")
		for taskID, msg := range sc.completed {
			b.WriteString(fmt.Sprintf("    <done task=%q from=%q timestamp=%q/>\n",
				taskID, msg.From, msg.Timestamp.Format(time.RFC3339)))
		}
		b.WriteString("  </completed>\n")
	}

	b.WriteString("</swarm-context>")
	return b.String()
}
