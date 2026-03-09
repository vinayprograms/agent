package executor

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSwarmContext_UpdateAgent(t *testing.T) {
	sc := NewSwarmContext()
	now := time.Now()

	sc.UpdateAgent("backend", "executing", "golang", "task-123", now)
	sc.UpdateAgent("frontend", "monitoring", "react", "", now)

	states := sc.GetAgentStates()
	if len(states) != 2 {
		t.Fatalf("Expected 2 agents, got %d", len(states))
	}

	// Update overwrites
	sc.UpdateAgent("backend", "monitoring", "golang", "", now.Add(time.Second))
	states = sc.GetAgentStates()
	for _, s := range states {
		if s.AgentID == "backend" && s.Status != "monitoring" {
			t.Errorf("Expected backend status monitoring, got %s", s.Status)
		}
	}
}

func TestSwarmContext_Discussion(t *testing.T) {
	sc := NewSwarmContext()
	now := time.Now()

	sc.AddDiscussMessage("task-123", DiscussMessage{
		From: "backend", Timestamp: now, Content: "I'll handle the API", Signal: "CLAIM",
	})
	sc.AddDiscussMessage("task-123", DiscussMessage{
		From: "frontend", Timestamp: now.Add(time.Second), Content: "I'll do the UI", Signal: "CLAIM",
	})

	msgs := sc.GetDiscussion("task-123")
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Signal != "CLAIM" {
		t.Errorf("Expected CLAIM signal, got %s", msgs[0].Signal)
	}

	// Non-existent task
	msgs = sc.GetDiscussion("task-999")
	if msgs != nil {
		t.Errorf("Expected nil for unknown task, got %v", msgs)
	}
}

func TestSwarmContext_Completed(t *testing.T) {
	sc := NewSwarmContext()
	now := time.Now()

	sc.AddDiscussMessage("task-123", DiscussMessage{
		From: "backend", Timestamp: now, Content: "API complete", Signal: "DONE",
	})

	completed := sc.GetCompleted()
	if len(completed) != 1 {
		t.Fatalf("Expected 1 completed, got %d", len(completed))
	}
	if completed["task-123"].From != "backend" {
		t.Errorf("Expected backend, got %s", completed["task-123"].From)
	}
}

func TestSwarmContext_ConcurrentAccess(t *testing.T) {
	sc := NewSwarmContext()
	var wg sync.WaitGroup

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sc.UpdateAgent("agent", "monitoring", "cap", "", time.Now())
				sc.AddDiscussMessage("task", DiscussMessage{Content: "msg"})
			}
		}(i)
	}

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sc.GetAgentStates()
				sc.GetDiscussion("task")
				sc.GetCompleted()
				sc.FormatForLLM("task")
			}
		}()
	}

	wg.Wait()
}

func TestSwarmContext_FormatForLLM(t *testing.T) {
	sc := NewSwarmContext()
	now := time.Now()

	sc.UpdateAgent("backend", "executing", "golang", "task-123", now)
	sc.UpdateAgent("frontend", "monitoring", "react", "", now)

	sc.AddDiscussMessage("task-123", DiscussMessage{
		From: "backend", Timestamp: now, Content: "CLAIM: API routes", Signal: "CLAIM",
	})

	sc.AddDiscussMessage("task-456", DiscussMessage{
		From: "frontend", Timestamp: now, Content: "UI complete", Signal: "DONE",
	})

	result := sc.FormatForLLM("task-123")

	if !strings.Contains(result, "<swarm-context>") {
		t.Error("Missing opening tag")
	}
	if !strings.Contains(result, "</swarm-context>") {
		t.Error("Missing closing tag")
	}
	if !strings.Contains(result, `id="backend"`) {
		t.Error("Missing backend agent")
	}
	if !strings.Contains(result, `id="frontend"`) {
		t.Error("Missing frontend agent")
	}
	if !strings.Contains(result, `<discussion task="task-123">`) {
		t.Error("Missing relevant discussion")
	}
	if !strings.Contains(result, "CLAIM: API routes") {
		t.Error("Missing discussion content")
	}
	if !strings.Contains(result, `signal="CLAIM"`) {
		t.Error("Missing signal attribute")
	}
	if !strings.Contains(result, "<completed>") {
		t.Error("Missing completed section")
	}
	if !strings.Contains(result, `<done task="task-456"`) {
		t.Error("Missing completed task")
	}

	// Should NOT include task-456 discussion (not relevant)
	if strings.Contains(result, `<discussion task="task-456">`) {
		t.Error("Should not include irrelevant task discussion")
	}
}

func TestSwarmContext_FormatEmpty(t *testing.T) {
	sc := NewSwarmContext()
	result := sc.FormatForLLM("")

	if !strings.Contains(result, "<swarm-context>") {
		t.Error("Missing opening tag")
	}
	if !strings.Contains(result, "<agents>") {
		t.Error("Missing agents section")
	}
	// No discussion or completed sections when empty
	if strings.Contains(result, "<discussion") {
		t.Error("Should not have discussion section when empty")
	}
	if strings.Contains(result, "<completed>") {
		t.Error("Should not have completed section when empty")
	}
}
