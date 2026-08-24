package session

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnknownFormat is returned by [DetectFormat] and [ReadFile] for a file
// that is neither JSONL nor legacy JSON.
var ErrUnknownFormat = errors.New("session: unknown file format")

// Recorder persists sessions to a directory, one append-only
// <id>.jsonl file per session. Safe for concurrent use.
type Recorder struct {
	dir  string
	sink Sink
}

// Open returns a Recorder for dir, creating it if needed. sink (may be
// nil) receives every event added to sessions this Recorder creates.
func Open(dir string, sink Sink) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: open %s: %w", dir, err)
	}
	return &Recorder{dir: dir, sink: sink}, nil
}

// Meta is the identity of a new session: the workflow NAME, the absolute
// path of the Agentfile it came from (empty for an inline goal), the
// deployment label (empty for a plain run), and the workflow inputs.
type Meta struct {
	Name      string
	Agentfile string
	Label     string
	Inputs    map[string]string
}

// Create starts a new running session for meta, writes its header, and
// starts the background writer. Call Session.Close when done.
func (r *Recorder) Create(meta Meta) (*Session, error) {
	now := time.Now()
	inputs := meta.Inputs
	if inputs == nil {
		inputs = make(map[string]string)
	}
	s := &Session{
		ID:           randomHex(16),
		WorkflowName: meta.Name,
		Agentfile:    meta.Agentfile,
		Label:        meta.Label,
		Inputs:       inputs,
		State:        make(map[string]any),
		Outputs:      make(map[string]string),
		Status:       StatusRunning,
		Events:       []Event{},
		CreatedAt:    now,
		UpdatedAt:    now,
		sink:         r.sink,
	}
	if err := r.save(s); err != nil {
		return nil, err
	}
	s.w = startWriter(r, s)
	return s, nil
}

// Update appends s's unwritten events and a new footer to its file,
// creating the file (with header) if needed. The last footer wins on read.
func (r *Recorder) Update(s *Session) error {
	s.mu.Lock()
	s.UpdatedAt = time.Now()
	s.mu.Unlock()
	return r.save(s)
}

// Get reads session id from disk — <id>.jsonl, or legacy <id>.json.
// The result has no writer: AddEvent appends in memory.
func (r *Recorder) Get(id string) (*Session, error) {
	path := filepath.Join(r.dir, id+".jsonl")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(r.dir, id+".json")
	}
	return ReadFile(path, ReadOptions{})
}

// JSONL record types.
const (
	recordHeader = "header" // session metadata (first line)
	recordEvent  = "event"  // one Event
	recordFooter = "footer" // final state (last footer wins)
)

// jsonlRecord is one JSONL line, discriminated by _type. The field set
// and tags are the on-disk wire format: do not change them.
type jsonlRecord struct {
	RecordType string `json:"_type"` // header, event, footer

	// Header fields (when _type == "header")
	ID           string            `json:"id,omitempty"`
	WorkflowName string            `json:"workflow_name,omitempty"`
	Agentfile    string            `json:"agentfile,omitempty"`
	Label        string            `json:"label,omitempty"`
	Inputs       map[string]string `json:"inputs,omitempty"`
	// Pointers so omitempty actually elides them: encoding/json does NOT
	// omit a zero time.Time, so value fields here serialised
	// "0001-01-01T00:00:00Z" onto every event record, which read as a
	// missing timestamp when events in fact carry their own Timestamp.
	CreatedAt    *time.Time        `json:"created_at,omitempty"`

	// Event fields (when _type == "event") - embedded Event
	*Event `json:",omitempty"`

	// Footer fields (when _type == "footer")
	Status    string            `json:"status,omitempty"`
	Result    string            `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	State     map[string]any    `json:"state,omitempty"`
	UpdatedAt *time.Time        `json:"updated_at,omitempty"`
}

// save appends to s's file: header on first write, then only the events
// not yet written, then a footer. Holds s.mu while reading Events, so it
// is safe against concurrent AddEvent; callers must not hold s.mu.
func (r *Recorder) save(s *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(r.dir, s.ID+".jsonl")
	_, statErr := os.Stat(path)
	isNew := statErr != nil

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: open file: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	createdAt, updatedAt := s.CreatedAt, s.UpdatedAt

	var recs []jsonlRecord
	if isNew {
		recs = append(recs, jsonlRecord{
			RecordType:   recordHeader,
			ID:           s.ID,
			WorkflowName: s.WorkflowName,
			Agentfile:    s.Agentfile,
			Label:        s.Label,
			Inputs:       s.Inputs,
			CreatedAt:    &createdAt,
		})
	}
	for i := s.written; i < len(s.Events); i++ {
		recs = append(recs, jsonlRecord{RecordType: recordEvent, Event: &s.Events[i]})
	}
	recs = append(recs, jsonlRecord{
		RecordType: recordFooter,
		Status:     s.Status,
		Result:     s.Result,
		Error:      s.Error,
		Outputs:    s.Outputs,
		State:      s.State,
		UpdatedAt:  &updatedAt,
	})
	for _, rec := range recs {
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("session: marshal %s record: %w", rec.RecordType, err)
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	if err := cmp.Or(w.Flush(), f.Sync()); err != nil {
		return fmt.Errorf("session: write file: %w", err)
	}
	s.written = len(s.Events)
	return nil
}

// ReadOptions tunes [ReadFile].
type ReadOptions struct {
	// MaxContentSize truncates any Event.Content longer than this many
	// bytes, appending a "[truncated, N bytes total]" marker. 0 = unlimited.
	MaxContentSize int
}

// ReadFile reads a session file in JSONL or legacy JSON format, detected
// per [DetectFormat].
func ReadFile(path string, opts ReadOptions) (*Session, error) {
	format, err := DetectFormat(path)
	if err != nil {
		return nil, err
	}
	var s *Session
	if format == "jsonl" {
		s, err = readJSONL(path)
	} else {
		s, err = readLegacyJSON(path)
	}
	if err != nil {
		return nil, err
	}
	if n := opts.MaxContentSize; n > 0 {
		for i := range s.Events {
			if c := s.Events[i].Content; len(c) > n {
				s.Events[i].Content = fmt.Sprintf("%s\n... [truncated, %d bytes total]", c[:n], len(c))
			}
		}
	}
	if len(s.Events) > 0 {
		s.seq.Store(s.Events[len(s.Events)-1].SeqID)
	}
	s.written = len(s.Events)
	return s, nil
}

// readJSONL reads the header/event/footer stream. Lines are unbounded in
// length (bufio.Reader, not Scanner); blank lines are skipped.
func readJSONL(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: read file: %w", err)
	}
	defer f.Close()

	s := &Session{
		Inputs:  make(map[string]string),
		State:   make(map[string]any),
		Outputs: make(map[string]string),
		Events:  []Event{},
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("session: read file: %w", err)
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if err := applyRecord(s, line); err != nil {
				return nil, err
			}
		}
		if err == io.EOF {
			return s, nil
		}
	}
}

// applyRecord parses one JSONL line into s.
func applyRecord(s *Session, line []byte) error {
	var rec jsonlRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return fmt.Errorf("session: parse JSONL line: %w", err)
	}
	switch rec.RecordType {
	case recordHeader:
		s.ID = rec.ID
		s.WorkflowName = rec.WorkflowName
		s.Agentfile = rec.Agentfile
		s.Label = rec.Label
		s.Inputs = rec.Inputs
		if rec.CreatedAt != nil {
			s.CreatedAt = *rec.CreatedAt
		}
	case recordEvent:
		if rec.Event != nil {
			s.Events = append(s.Events, *rec.Event)
		}
	case recordFooter:
		s.Status = rec.Status
		s.Result = rec.Result
		s.Error = rec.Error
		s.Outputs = rec.Outputs
		s.State = rec.State
		if rec.UpdatedAt != nil {
			s.UpdatedAt = *rec.UpdatedAt
		}
	}
	return nil
}

func readLegacyJSON(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session: read file: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session: parse legacy JSON: %w", err)
	}
	return &s, nil
}

// DetectFormat reports "jsonl" or "json" for path, by extension, else by
// sniffing the first 256 bytes ("_type" marks JSONL, "events" legacy
// JSON). Returns [ErrUnknownFormat] if neither applies.
func DetectFormat(path string) (string, error) {
	switch filepath.Ext(path) {
	case ".jsonl":
		return "jsonl", nil
	case ".json":
		return "json", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("session: detect format: %w", err)
	}
	defer f.Close()
	head := make([]byte, 256)
	n, _ := f.Read(head)
	content := string(head[:n])
	switch {
	case strings.Contains(content, `"_type"`):
		return "jsonl", nil
	case strings.Contains(content, `"events"`):
		return "json", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownFormat, path)
}

// randomHex returns n random bytes hex-encoded. crypto/rand.Read never
// returns an error (it crashes the program if the OS source fails).
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
