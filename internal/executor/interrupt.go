package executor

import (
	"fmt"
	"sync"
	"time"
)

// InterruptMessage represents a message received from discuss.* during execution.
type InterruptMessage struct {
	From      string    // Agent name or "operator"
	Timestamp time.Time // When the message was published
	Content   string    // Raw message content
	TaskID    string    // Task ID from the NATS subject
}

// InterruptBuffer is a thread-safe FIFO queue that collects discuss.*
// messages arriving during execution. The NATS subscriber writes to it;
// the executor drains it between LLM turns.
//
// A nil buffer is valid and indicates non-swarm mode — all operations
// short-circuit immediately with zero overhead.
type InterruptBuffer struct {
	mu       sync.Mutex
	messages []InterruptMessage
}

// NewInterruptBuffer creates a new interrupt buffer.
func NewInterruptBuffer() *InterruptBuffer {
	return &InterruptBuffer{}
}

// Push adds a message to the buffer. Safe to call from any goroutine.
func (b *InterruptBuffer) Push(msg InterruptMessage) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, msg)
}

// Drain removes and returns all buffered messages. Returns nil if empty.
// Safe to call from any goroutine.
func (b *InterruptBuffer) Drain() []InterruptMessage {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.messages) == 0 {
		return nil
	}
	msgs := b.messages
	b.messages = nil
	return msgs
}

// Len returns the current buffer size.
func (b *InterruptBuffer) Len() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages)
}

// FormatInterruptsBlock formats buffered interrupt messages into an XML
// block for injection into the LLM's next turn. The goalDescription
// provides the <context> element — just the current goal, nothing more.
func FormatInterruptsBlock(goalDescription string, interrupts []InterruptMessage) string {
	xml := "<interrupts>\n"
	xml += "  <context>\n"
	xml += fmt.Sprintf("    Current goal: %s\n", goalDescription)
	xml += "  </context>\n\n"

	xml += "  <messages>\n"
	for _, msg := range interrupts {
		xml += fmt.Sprintf("    <message from=%q timestamp=%q>\n",
			msg.From, msg.Timestamp.Format(time.RFC3339))
		xml += fmt.Sprintf("      %s\n", msg.Content)
		xml += "    </message>\n"
	}
	xml += "  </messages>\n\n"

	xml += `  <guidance>
    The above messages arrived while you were executing. Evaluate
    them against your current work and decide how to proceed.
    You may continue working if the messages are irrelevant,
    adjust your approach if they affect your remaining work,
    or stop execution if your work is no longer viable.

    If you stop, explain the reason in your response. This
    explanation will be published to the swarm so other agents
    understand why work was stopped and can adapt accordingly.
  </guidance>
`
	xml += "</interrupts>"
	return xml
}
