package replay

import (
	"github.com/vinayprograms/agent/internal/session"
)

// loadSession reads a session file (JSONL or legacy JSON), truncating
// oversized Content fields per r.maxContentSize.
func (r *Replayer) loadSession(path string) (*session.Session, error) {
	return session.ReadFile(path, session.ReadOptions{MaxContentSize: r.maxContentSize})
}
