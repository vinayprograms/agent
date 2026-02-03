package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore stores sessions in SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// init creates the database schema.
func (s *SQLiteStore) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		workflow_name TEXT NOT NULL,
		inputs TEXT,
		state TEXT,
		outputs TEXT,
		status TEXT NOT NULL,
		result TEXT,
		error TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		type TEXT NOT NULL,
		goal TEXT,
		content TEXT,
		tool TEXT,
		args TEXT,
		error TEXT,
		duration_ms INTEGER,
		timestamp DATETIME NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// Save saves a session to the database.
func (s *SQLiteStore) Save(sess *Session) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	inputsJSON, _ := json.Marshal(sess.Inputs)
	stateJSON, _ := json.Marshal(sess.State)
	outputsJSON, _ := json.Marshal(sess.Outputs)

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, workflow_name, inputs, state, outputs, status, result, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			state = excluded.state,
			outputs = excluded.outputs,
			status = excluded.status,
			result = excluded.result,
			error = excluded.error,
			updated_at = excluded.updated_at
	`, sess.ID, sess.WorkflowName, string(inputsJSON), string(stateJSON), string(outputsJSON),
		string(sess.Status), sess.Result, sess.Error, sess.CreatedAt, sess.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Delete existing events (full replacement)
	_, err = tx.Exec("DELETE FROM events WHERE session_id = ?", sess.ID)
	if err != nil {
		return fmt.Errorf("failed to delete events: %w", err)
	}

	// Insert events
	for _, event := range sess.Events {
		argsJSON, _ := json.Marshal(event.Args)
		_, err = tx.Exec(`
			INSERT INTO events (session_id, type, goal, content, tool, args, error, duration_ms, timestamp)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, sess.ID, event.Type, event.Goal, event.Content, event.Tool, 
			string(argsJSON), event.Error, event.DurationMs, event.Timestamp)
		if err != nil {
			return fmt.Errorf("failed to save event: %w", err)
		}
	}

	return tx.Commit()
}

// Load loads a session from the database.
func (s *SQLiteStore) Load(id string) (*Session, error) {
	row := s.db.QueryRow(`
		SELECT id, workflow_name, inputs, state, outputs, status, result, error, created_at, updated_at
		FROM sessions WHERE id = ?
	`, id)

	var sess Session
	var inputsJSON, stateJSON, outputsJSON string
	var status string

	err := row.Scan(&sess.ID, &sess.WorkflowName, &inputsJSON, &stateJSON, &outputsJSON,
		&status, &sess.Result, &sess.Error, &sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found: %s", id)
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	sess.Status = status
	json.Unmarshal([]byte(inputsJSON), &sess.Inputs)
	json.Unmarshal([]byte(stateJSON), &sess.State)
	json.Unmarshal([]byte(outputsJSON), &sess.Outputs)

	// Load events
	rows, err := s.db.Query(`
		SELECT type, goal, content, tool, args, error, duration_ms, timestamp
		FROM events WHERE session_id = ? ORDER BY id
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}
	defer rows.Close()

	sess.Events = []Event{}
	for rows.Next() {
		var event Event
		var goal, content, tool, argsJSON, eventError sql.NullString
		var durationMs sql.NullInt64
		err := rows.Scan(&event.Type, &goal, &content, &tool, &argsJSON, &eventError, &durationMs, &event.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if goal.Valid {
			event.Goal = goal.String
		}
		if content.Valid {
			event.Content = content.String
		}
		if tool.Valid {
			event.Tool = tool.String
		}
		if argsJSON.Valid && argsJSON.String != "" && argsJSON.String != "null" {
			json.Unmarshal([]byte(argsJSON.String), &event.Args)
		}
		if eventError.Valid {
			event.Error = eventError.String
		}
		if durationMs.Valid {
			event.DurationMs = durationMs.Int64
		}
		sess.Events = append(sess.Events, event)
	}

	return &sess, nil
}

// SQLiteManager wraps SQLiteStore to implement SessionManager.
type SQLiteManager struct {
	store *SQLiteStore
}

// NewSQLiteManager creates a new SQLite-based session manager.
func NewSQLiteManager(path string) (SessionManager, error) {
	store, err := NewSQLiteStore(path)
	if err != nil {
		return nil, err
	}
	return &SQLiteManager{store: store}, nil
}

// Create creates a new session.
func (m *SQLiteManager) Create(workflowName string) (*Session, error) {
	id := generateID()
	now := time.Now()

	sess := &Session{
		ID:           id,
		WorkflowName: workflowName,
		Inputs:       make(map[string]string),
		State:        make(map[string]interface{}),
		Outputs:      make(map[string]string),
		Status:       "running",
		Events:       []Event{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := m.store.Save(sess); err != nil {
		return nil, err
	}

	return sess, nil
}

// Update updates a session.
func (m *SQLiteManager) Update(sess *Session) error {
	sess.UpdatedAt = time.Now()
	return m.store.Save(sess)
}

// Get retrieves a session by ID.
func (m *SQLiteManager) Get(id string) (*Session, error) {
	return m.store.Load(id)
}
