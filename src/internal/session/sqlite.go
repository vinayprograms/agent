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
		status TEXT NOT NULL,
		result TEXT,
		error TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		goal TEXT,
		agent TEXT,
		timestamp DATETIME NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE TABLE IF NOT EXISTS tool_calls (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		name TEXT NOT NULL,
		args TEXT,
		result TEXT,
		error TEXT,
		duration_ns INTEGER,
		goal TEXT,
		timestamp DATETIME NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id)
	);

	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
	CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
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

	// Upsert session
	_, err = tx.Exec(`
		INSERT INTO sessions (id, workflow_name, inputs, state, status, result, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			state = excluded.state,
			status = excluded.status,
			result = excluded.result,
			error = excluded.error,
			updated_at = excluded.updated_at
	`, sess.ID, sess.WorkflowName, string(inputsJSON), string(stateJSON),
		string(sess.Status), sess.Result, sess.Error, sess.CreatedAt, sess.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Delete existing messages and tool calls (full replacement)
	_, err = tx.Exec("DELETE FROM messages WHERE session_id = ?", sess.ID)
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}
	_, err = tx.Exec("DELETE FROM tool_calls WHERE session_id = ?", sess.ID)
	if err != nil {
		return fmt.Errorf("failed to delete tool calls: %w", err)
	}

	// Insert messages
	for _, msg := range sess.Messages {
		_, err = tx.Exec(`
			INSERT INTO messages (session_id, role, content, goal, agent, timestamp)
			VALUES (?, ?, ?, ?, ?, ?)
		`, sess.ID, msg.Role, msg.Content, msg.Goal, msg.Agent, msg.Timestamp)
		if err != nil {
			return fmt.Errorf("failed to save message: %w", err)
		}
	}

	// Insert tool calls
	for _, tc := range sess.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Args)
		resultJSON, _ := json.Marshal(tc.Result)
		_, err = tx.Exec(`
			INSERT INTO tool_calls (id, session_id, name, args, result, error, duration_ns, goal, timestamp)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, tc.ID, sess.ID, tc.Name, string(argsJSON), string(resultJSON),
			tc.Error, int64(tc.Duration), tc.Goal, tc.Timestamp)
		if err != nil {
			return fmt.Errorf("failed to save tool call: %w", err)
		}
	}

	return tx.Commit()
}

// Load loads a session from the database.
func (s *SQLiteStore) Load(id string) (*Session, error) {
	row := s.db.QueryRow(`
		SELECT id, workflow_name, inputs, state, status, result, error, created_at, updated_at
		FROM sessions WHERE id = ?
	`, id)

	var sess Session
	var inputsJSON, stateJSON string
	var status string

	err := row.Scan(&sess.ID, &sess.WorkflowName, &inputsJSON, &stateJSON,
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

	// Load messages
	rows, err := s.db.Query(`
		SELECT role, content, goal, agent, timestamp
		FROM messages WHERE session_id = ? ORDER BY id
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var msg Message
		var agent sql.NullString
		err := rows.Scan(&msg.Role, &msg.Content, &msg.Goal, &agent, &msg.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		if agent.Valid {
			msg.Agent = agent.String
		}
		sess.Messages = append(sess.Messages, msg)
	}

	// Load tool calls
	rows, err = s.db.Query(`
		SELECT id, name, args, result, error, duration_ns, goal, timestamp
		FROM tool_calls WHERE session_id = ? ORDER BY timestamp
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load tool calls: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tc ToolCall
		var argsJSON, resultJSON string
		var durationNs int64
		var tcError sql.NullString
		err := rows.Scan(&tc.ID, &tc.Name, &argsJSON, &resultJSON,
			&tcError, &durationNs, &tc.Goal, &tc.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to scan tool call: %w", err)
		}
		json.Unmarshal([]byte(argsJSON), &tc.Args)
		json.Unmarshal([]byte(resultJSON), &tc.Result)
		if tcError.Valid {
			tc.Error = tcError.String
		}
		tc.Duration = time.Duration(durationNs)
		sess.ToolCalls = append(sess.ToolCalls, tc)
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
		Messages:     []Message{},
		ToolCalls:    []ToolCall{},
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
