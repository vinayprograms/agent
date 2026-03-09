package executor

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInterruptBuffer_NilSafety(t *testing.T) {
	var b *InterruptBuffer

	// All operations on nil buffer should be safe and zero-cost
	b.Push(InterruptMessage{Content: "should not panic"})
	msgs := b.Drain()
	if msgs != nil {
		t.Errorf("Drain on nil buffer should return nil, got %v", msgs)
	}
	if b.Len() != 0 {
		t.Errorf("Len on nil buffer should return 0, got %d", b.Len())
	}
}

func TestInterruptBuffer_PushAndDrain(t *testing.T) {
	b := NewInterruptBuffer()

	// Empty drain
	msgs := b.Drain()
	if msgs != nil {
		t.Errorf("Drain on empty buffer should return nil, got %v", msgs)
	}

	// Push messages
	b.Push(InterruptMessage{From: "frontend", Content: "switching to WebSocket"})
	b.Push(InterruptMessage{From: "webserver", Content: "I'll handle the upgrade"})

	if b.Len() != 2 {
		t.Errorf("Expected Len 2, got %d", b.Len())
	}

	// Drain gets all messages
	msgs = b.Drain()
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].From != "frontend" {
		t.Errorf("Expected first message from frontend, got %s", msgs[0].From)
	}
	if msgs[1].From != "webserver" {
		t.Errorf("Expected second message from webserver, got %s", msgs[1].From)
	}

	// Buffer is empty after drain
	if b.Len() != 0 {
		t.Errorf("Expected Len 0 after drain, got %d", b.Len())
	}
	msgs = b.Drain()
	if msgs != nil {
		t.Errorf("Second drain should return nil, got %v", msgs)
	}
}

func TestInterruptBuffer_ConcurrentAccess(t *testing.T) {
	b := NewInterruptBuffer()
	var wg sync.WaitGroup

	// 10 goroutines pushing 100 messages each
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Push(InterruptMessage{From: "agent", Content: "msg"})
			}
		}(i)
	}

	wg.Wait()

	msgs := b.Drain()
	if len(msgs) != 1000 {
		t.Errorf("Expected 1000 messages after concurrent push, got %d", len(msgs))
	}
}

func TestFormatInterruptsBlock(t *testing.T) {
	interrupts := []InterruptMessage{
		{
			From:      "frontend",
			Timestamp: time.Date(2026, 3, 8, 14, 30, 0, 0, time.UTC),
			Content:   "Switching to WebSocket for notifications.",
		},
		{
			From:      "webserver",
			Timestamp: time.Date(2026, 3, 8, 14, 31, 0, 0, time.UTC),
			Content:   "I'll handle the upgrade headers.",
		},
	}

	result := FormatInterruptsBlock("Implement REST API route handlers", interrupts)

	// Check structure
	if !strings.Contains(result, "<interrupts>") {
		t.Error("Missing <interrupts> opening tag")
	}
	if !strings.Contains(result, "</interrupts>") {
		t.Error("Missing </interrupts> closing tag")
	}
	if !strings.Contains(result, "Current goal: Implement REST API route handlers") {
		t.Error("Missing goal description in context")
	}
	if !strings.Contains(result, `from="frontend"`) {
		t.Error("Missing frontend attribution")
	}
	if !strings.Contains(result, `from="webserver"`) {
		t.Error("Missing webserver attribution")
	}
	if !strings.Contains(result, "Switching to WebSocket") {
		t.Error("Missing message content")
	}
	if !strings.Contains(result, "<guidance>") {
		t.Error("Missing guidance block")
	}
}
