package swarm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/vinayprograms/swarmkit/messaging"
)

// fakeBus records publishes; it implements messaging.Bus in-memory.
// Each Publish (successful or not) sends on published if it is non-nil.
type fakeBus struct {
	mu        sync.Mutex
	messages_ []messaging.Message
	err       error         // returned from Publish when set
	published chan struct{} // optional: receives one value per Publish call
}

func (b *fakeBus) Publish(subject string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.published != nil {
		b.published <- struct{}{}
	}
	if b.err != nil {
		return b.err
	}
	b.messages_ = append(b.messages_, messaging.Message{Subject: subject, Data: data})
	return nil
}

func (b *fakeBus) messages() []messaging.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]messaging.Message(nil), b.messages_...)
}

func (b *fakeBus) Subscribe(string) (messaging.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (b *fakeBus) Join(string) messaging.Joiner { return nil }
func (b *fakeBus) Request(string, []byte, time.Duration) (*messaging.Message, error) {
	return nil, errors.New("not implemented")
}
func (b *fakeBus) Shutdown(context.Context) error { return nil }
func (b *fakeBus) Close() error                   { return nil }
