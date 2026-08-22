package session

import (
	"sync"
	"time"
)

// Writer tuning.
const (
	eventChSize   = 256 // buffered event channel capacity
	batchSizeMax  = 50  // persist when a batch reaches this size
	flushInterval = 500 * time.Millisecond
)

// writer batches a session's events to disk on a background goroutine.
// It persists when a batch reaches batchSizeMax, every flushInterval, on
// flush, and on close.
type writer struct {
	rec    *Recorder
	sess   *Session
	events chan Event
	flushq chan chan struct{}
	stop   chan struct{}
	done   chan struct{} // closed when loop exits

	// mu makes close mutually exclusive with in-flight enqueue/flush, so an
	// event is never left in the channel after the loop has drained it.
	mu     sync.RWMutex
	closed bool
}

func startWriter(rec *Recorder, sess *Session) *writer {
	w := &writer{
		rec:    rec,
		sess:   sess,
		events: make(chan Event, eventChSize),
		flushq: make(chan chan struct{}),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.loop()
	return w
}

// enqueue hands ev to the loop. Reports false once closed.
func (w *writer) enqueue(ev Event) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return false
	}
	w.events <- ev
	return true
}

// flush blocks until everything enqueued so far is persisted.
func (w *writer) flush() {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return
	}
	done := make(chan struct{})
	w.flushq <- done
	<-done
}

// close persists everything enqueued and stops the loop. Idempotent.
func (w *writer) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.stop)
	<-w.done
}

func (w *writer) loop() {
	defer close(w.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var batch []Event
	persist := func() {
		if len(batch) == 0 {
			return
		}
		w.sess.append(batch...)
		// Best effort: a failed batch stays in memory and the caller's final
		// Update reports the error.
		w.rec.Update(w.sess)
		batch = batch[:0]
	}
	for {
		select {
		case ev := <-w.events:
			batch = append(batch, ev)
			if len(batch) >= batchSizeMax {
				persist()
			}
		case <-ticker.C:
			persist()
		case done := <-w.flushq:
			batch = w.drain(batch)
			persist()
			close(done)
		case <-w.stop:
			batch = w.drain(batch)
			persist()
			return
		}
	}
}

// drain appends every event already in the channel to batch without blocking.
func (w *writer) drain(batch []Event) []Event {
	for {
		select {
		case ev := <-w.events:
			batch = append(batch, ev)
		default:
			return batch
		}
	}
}
