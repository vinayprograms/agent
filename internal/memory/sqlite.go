package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

func init() {
	sqlite_vec.Auto()
}

// SQLiteStore implements Store using SQLite with sqlite-vec for vector search.
type SQLiteStore struct {
	db        *sql.DB
	embedder  EmbeddingProvider
	dimension int
}

// SQLiteConfig configures the SQLite memory store.
type SQLiteConfig struct {
	Path      string
	Embedder  EmbeddingProvider
}

// NewSQLiteStore creates a new SQLite-based memory store.
func NewSQLiteStore(cfg SQLiteConfig) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{
		db:        db,
		embedder:  cfg.Embedder,
		dimension: cfg.Embedder.Dimension(),
	}

	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

// init creates the database schema.
func (s *SQLiteStore) init() error {
	// Check sqlite-vec is loaded
	var vecVersion string
	err := s.db.QueryRow("SELECT vec_version()").Scan(&vecVersion)
	if err != nil {
		return fmt.Errorf("sqlite-vec not loaded: %w", err)
	}

	schema := fmt.Sprintf(`
	-- Semantic memories
	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		source TEXT,
		importance REAL DEFAULT 0.5,
		created_at DATETIME NOT NULL,
		accessed_at DATETIME NOT NULL,
		access_count INTEGER DEFAULT 0,
		tags TEXT
	);

	-- Vector embeddings for semantic search
	CREATE VIRTUAL TABLE IF NOT EXISTS memory_vectors USING vec0(
		id TEXT PRIMARY KEY,
		embedding FLOAT[%d]
	);

	-- Key-value store for structured data (backward compatibility)
	CREATE TABLE IF NOT EXISTS memory_kv (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source);
	CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at);
	`, s.dimension)

	_, err = s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

// Remember stores a memory with embedding.
func (s *SQLiteStore) Remember(ctx context.Context, content string, meta MemoryMetadata) error {
	// Generate embedding
	embeddings, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return fmt.Errorf("empty embedding returned")
	}

	id := uuid.New().String()
	now := time.Now()

	importance := meta.Importance
	if importance == 0 {
		importance = 0.5
	}

	var tagsJSON []byte
	if len(meta.Tags) > 0 {
		tagsJSON, _ = json.Marshal(meta.Tags)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert memory
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memories (id, content, source, importance, created_at, accessed_at, access_count, tags)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?)
	`, id, content, meta.Source, importance, now, now, string(tagsJSON))
	if err != nil {
		return fmt.Errorf("failed to insert memory: %w", err)
	}

	// Insert embedding
	embeddingBlob := serializeEmbedding(embeddings[0])
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_vectors (id, embedding)
		VALUES (?, ?)
	`, id, embeddingBlob)
	if err != nil {
		return fmt.Errorf("failed to insert embedding: %w", err)
	}

	return tx.Commit()
}

// Recall performs semantic search for relevant memories.
func (s *SQLiteStore) Recall(ctx context.Context, query string, opts RecallOpts) ([]MemoryResult, error) {
	// Generate query embedding
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	queryBlob := serializeEmbedding(embeddings[0])

	// Vector similarity search
	rows, err := s.db.QueryContext(ctx, `
		SELECT 
			m.id, m.content, m.source, m.importance, 
			m.created_at, m.accessed_at, m.access_count, m.tags,
			v.distance
		FROM memory_vectors v
		JOIN memories m ON v.id = m.id
		WHERE v.embedding MATCH ?
		  AND k = ?
		ORDER BY v.distance
	`, queryBlob, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}
	defer rows.Close()

	var results []MemoryResult
	for rows.Next() {
		var m MemoryResult
		var tagsJSON sql.NullString
		var distance float32

		err := rows.Scan(
			&m.ID, &m.Content, &m.Source, &m.Importance,
			&m.CreatedAt, &m.AccessedAt, &m.AccessCount, &tagsJSON,
			&distance,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert distance to similarity score
		// sqlite-vec uses L2 distance by default, lower is better
		// We normalize: score = 1 / (1 + distance)
		if distance < 0 {
			distance = 0
		}
		m.Score = 1.0 / (1.0 + distance)

		if tagsJSON.Valid {
			json.Unmarshal([]byte(tagsJSON.String), &m.Tags)
		}

		// Apply filters
		if opts.MinScore > 0 && m.Score < opts.MinScore {
			continue
		}

		if opts.TimeRange != nil {
			if m.CreatedAt.Before(opts.TimeRange.Start) || m.CreatedAt.After(opts.TimeRange.End) {
				continue
			}
		}

		if len(opts.Tags) > 0 {
			if !hasAnyTag(m.Tags, opts.Tags) {
				continue
			}
		}

		results = append(results, m)

		// Update access stats
		go s.updateAccessStats(m.ID)
	}

	return results, nil
}

// Forget deletes a memory by ID.
func (s *SQLiteStore) Forget(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM memory_vectors WHERE id = ?", id)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Get retrieves a value by key (key-value store).
func (s *SQLiteStore) Get(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM memory_kv WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("key not found: %s", key)
	}
	return value, err
}

// Set stores a key-value pair.
func (s *SQLiteStore) Set(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO memory_kv (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = ?
	`, key, value, time.Now(), value, time.Now())
	return err
}

// List returns keys matching a prefix.
func (s *SQLiteStore) List(prefix string) ([]string, error) {
	query := "SELECT key FROM memory_kv"
	args := []interface{}{}

	if prefix != "" {
		query += " WHERE key LIKE ?"
		args = append(args, prefix+"%")
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// Search performs substring search on key-value store.
func (s *SQLiteStore) Search(query string) ([]SearchResult, error) {
	rows, err := s.db.Query(`
		SELECT key, value FROM memory_kv 
		WHERE key LIKE ? OR value LIKE ?
	`, "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Key, &r.Value); err != nil {
			return nil, err
		}
		results = append(results, r)
	}

	return results, nil
}

// ConsolidateSession extracts and stores insights from a session transcript.
func (s *SQLiteStore) ConsolidateSession(ctx context.Context, sessionID string, transcript []Message) error {
	if len(transcript) == 0 {
		return nil
	}

	// Extract key content from the session
	var insights []string

	// Look for decisions, conclusions, and important facts
	for _, msg := range transcript {
		content := msg.Content
		lower := strings.ToLower(content)

		// Heuristic: messages containing decision/conclusion language
		if containsAny(lower, []string{
			"decided", "conclusion", "important", "remember",
			"note that", "key insight", "learned that",
			"will use", "should use", "agreed",
		}) {
			insights = append(insights, content)
		}
	}

	// Also include the last assistant message as a summary
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i].Role == "assistant" && len(transcript[i].Content) > 100 {
			insights = append(insights, transcript[i].Content)
			break
		}
	}

	// Store each insight
	for _, insight := range insights {
		if len(insight) < 50 {
			continue // Skip very short content
		}

		// Truncate very long content
		if len(insight) > 2000 {
			insight = insight[:2000] + "..."
		}

		err := s.Remember(ctx, insight, MemoryMetadata{
			Source:     "session:" + sessionID,
			Importance: 0.6,
		})
		if err != nil {
			// Log but don't fail consolidation
			continue
		}
	}

	return nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// updateAccessStats updates access count and time for a memory.
func (s *SQLiteStore) updateAccessStats(id string) {
	s.db.Exec(`
		UPDATE memories 
		SET accessed_at = ?, access_count = access_count + 1 
		WHERE id = ?
	`, time.Now(), id)
}

// serializeEmbedding converts a float32 slice to bytes for sqlite-vec.
func serializeEmbedding(embedding []float32) []byte {
	data, _ := sqlite_vec.SerializeFloat32(embedding)
	return data
}

// hasAnyTag checks if the memory has any of the filter tags.
func hasAnyTag(memTags, filterTags []string) bool {
	tagSet := make(map[string]bool)
	for _, t := range memTags {
		tagSet[t] = true
	}
	for _, t := range filterTags {
		if tagSet[t] {
			return true
		}
	}
	return false
}

// containsAny checks if text contains any of the patterns.
func containsAny(text string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
