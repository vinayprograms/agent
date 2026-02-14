package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/vinayprograms/agent/internal/session"
)

// loadSession loads a session from a file, detecting format automatically.
func (r *Replayer) loadSession(path string) (*session.Session, error) {
	format, err := session.DetectFormat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to detect format: %w", err)
	}

	if format == "jsonl" {
		return r.loadJSONL(path)
	}
	return r.loadLegacyJSON(path)
}

// loadJSONL loads a session from JSONL format (streaming).
func (r *Replayer) loadJSONL(path string) (*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	defer f.Close()

	sess := &session.Session{
		Inputs:  make(map[string]string),
		State:   make(map[string]interface{}),
		Outputs: make(map[string]string),
		Events:  []session.Event{},
	}

	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(line) > 0 {
					if parseErr := r.parseJSONLLine(line, sess); parseErr != nil {
						return nil, parseErr
					}
				}
				break
			}
			return nil, fmt.Errorf("error reading JSONL: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if err := r.parseJSONLLine(line, sess); err != nil {
			return nil, err
		}
	}

	return sess, nil
}

// parseJSONLLine parses a single JSONL line into the session.
func (r *Replayer) parseJSONLLine(line []byte, sess *session.Session) error {
	var record session.JSONLRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return fmt.Errorf("failed to parse JSONL line: %w", err)
	}

	switch record.RecordType {
	case session.RecordTypeHeader:
		sess.ID = record.ID
		sess.WorkflowName = record.WorkflowName
		sess.Inputs = record.Inputs
		sess.CreatedAt = record.CreatedAt

	case session.RecordTypeEvent:
		if record.Event != nil {
			evt := *record.Event
			if r.maxContentSize > 0 && len(evt.Content) > r.maxContentSize {
				evt.Content = evt.Content[:r.maxContentSize] +
					fmt.Sprintf("\n... [truncated, %d bytes total]", len(record.Event.Content))
			}
			sess.Events = append(sess.Events, evt)
		}

	case session.RecordTypeFooter:
		sess.Status = record.Status
		sess.Result = record.Result
		sess.Error = record.Error
		sess.Outputs = record.Outputs
		sess.State = record.State
		sess.UpdatedAt = record.UpdatedAt
	}

	return nil
}

// loadLegacyJSON loads a session from legacy JSON format.
func (r *Replayer) loadLegacyJSON(path string) (*session.Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var sess session.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("failed to parse session: %w", err)
	}

	if r.maxContentSize > 0 {
		for i := range sess.Events {
			if len(sess.Events[i].Content) > r.maxContentSize {
				originalSize := len(sess.Events[i].Content)
				sess.Events[i].Content = sess.Events[i].Content[:r.maxContentSize] +
					fmt.Sprintf("\n... [truncated, %d bytes total]", originalSize)
			}
		}
	}

	return &sess, nil
}
