package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Summary is the identity and outcome of one recorded session: everything
// [Find] can learn from a file's header and footer, without reading its
// events.
type Summary struct {
	ID        string
	Path      string
	Name      string // workflow NAME
	Agentfile string // absolute Agentfile path; empty for an inline goal
	Label     string // deployment label; empty for a plain run
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Error     string
}

// Find walks root recursively and summarises every session file it holds
// (*.jsonl, plus legacy *.json), so both the flat <root>/<id>.jsonl layout
// and older nested ones are readable. Results are sorted oldest first, by
// CreatedAt and then by ID, so sessions recorded in the same instant
// still come back in a stable order.
//
// Files that cannot be read or parsed are skipped; Find still returns the
// summaries it did build, alongside a joined error naming each failure. A
// missing root is not an error — it yields no summaries.
func Find(root string) ([]Summary, error) {
	var (
		out  []Summary
		errs []error
	)
	// WalkDir only fails through the callback, which reports into errs.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return nil // a missing root simply holds no sessions
			}
			errs = append(errs, err)
			return nil
		case d.IsDir():
			return nil
		}
		switch filepath.Ext(path) {
		case ".jsonl", ".json":
		default:
			return nil
		}
		s, err := Summarize(path)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		out = append(out, s)
		return nil
	})
	slices.SortFunc(out, byCreation)
	return out, errors.Join(errs...)
}

// Summarize reads one session file's identity and outcome from its
// header and last footer, without parsing its events. It accepts both
// JSONL and legacy JSON files.
func Summarize(path string) (Summary, error) {
	if filepath.Ext(path) == ".json" {
		s, err := ReadFile(path, ReadOptions{})
		if err != nil {
			return Summary{}, fmt.Errorf("session: summarize %s: %w", path, err)
		}
		return Summary{
			ID: s.ID, Path: path, Name: s.WorkflowName, Agentfile: s.Agentfile,
			Label: s.Label, Status: s.Status, CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt, Error: s.Error,
		}, nil
	}
	return summarizeJSONL(path)
}

// Record-type prefixes: jsonlRecord marshals _type first, so a line's kind
// is known without decoding it. Event lines are skipped on the prefix.
var (
	headerPrefix = []byte(`{"_type":"header"`)
	footerPrefix = []byte(`{"_type":"footer"`)
)

// summarizeJSONL decodes only the header and the last footer of a JSONL
// session file.
func summarizeJSONL(path string) (Summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, fmt.Errorf("session: summarize: %w", err)
	}
	defer f.Close()

	sum := Summary{Path: path}
	seenHeader := false
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return Summary{}, fmt.Errorf("session: summarize %s: %w", path, err)
		}
		line = bytes.TrimSpace(line)
		var rec jsonlRecord
		switch {
		case bytes.HasPrefix(line, headerPrefix):
			if err := json.Unmarshal(line, &rec); err != nil {
				return Summary{}, fmt.Errorf("session: summarize %s: %w", path, err)
			}
			sum.ID, sum.Name = rec.ID, rec.WorkflowName
			sum.Agentfile, sum.Label = rec.Agentfile, rec.Label
			sum.CreatedAt = rec.CreatedAt
			seenHeader = true
		case bytes.HasPrefix(line, footerPrefix):
			if err := json.Unmarshal(line, &rec); err != nil {
				return Summary{}, fmt.Errorf("session: summarize %s: %w", path, err)
			}
			sum.Status, sum.Error, sum.UpdatedAt = rec.Status, rec.Error, rec.UpdatedAt
		}
		if err == io.EOF {
			break
		}
	}
	if !seenHeader {
		return Summary{}, fmt.Errorf("session: summarize %s: no header record", path)
	}
	return sum, nil
}

// byCreation orders summaries oldest first, breaking ties on ID.
func byCreation(a, b Summary) int {
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}
