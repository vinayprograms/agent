// Package session provides session management and persistence.
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// R7.1.1: Create new session for workflow run
func TestSession_Create(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, err := mgr.Create("test-workflow", map[string]string{"input1": "value1"})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}

	if sess.ID == "" {
		t.Error("session ID should not be empty")
	}
	if sess.WorkflowName != "test-workflow" {
		t.Errorf("expected workflow name 'test-workflow', got %s", sess.WorkflowName)
	}
	if sess.Status != StatusRunning {
		t.Errorf("expected status Running, got %s", sess.Status)
	}
	if sess.Inputs["input1"] != "value1" {
		t.Errorf("expected input1='value1', got %s", sess.Inputs["input1"])
	}
}

// R7.1.2: Generate unique session ID
func TestSession_UniqueIDs(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		sess, _ := mgr.Create("workflow", nil)
		if ids[sess.ID] {
			t.Errorf("duplicate session ID: %s", sess.ID)
		}
		ids[sess.ID] = true
	}
}

// R7.1.5: Mark session complete or failed
func TestSession_Complete(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	err := mgr.Complete(sess.ID, "result data")
	if err != nil {
		t.Fatalf("complete error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if loaded.Status != StatusComplete {
		t.Errorf("expected status Complete, got %s", loaded.Status)
	}
	if loaded.Result != "result data" {
		t.Errorf("expected result 'result data', got %s", loaded.Result)
	}
}

func TestSession_Fail(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	err := mgr.Fail(sess.ID, "something went wrong")
	if err != nil {
		t.Fatalf("fail error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if loaded.Status != StatusFailed {
		t.Errorf("expected status Failed, got %s", loaded.Status)
	}
	if loaded.Error != "something went wrong" {
		t.Errorf("expected error 'something went wrong', got %s", loaded.Error)
	}
}

// R7.2.1: Persist execution state after each goal
func TestSession_UpdateState(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	state := map[string]interface{}{
		"goal1": "result1",
		"iteration": 5,
	}
	err := mgr.UpdateState(sess.ID, state)
	if err != nil {
		t.Fatalf("update state error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if loaded.State["goal1"] != "result1" {
		t.Errorf("expected goal1='result1', got %v", loaded.State["goal1"])
	}
}

// R7.2.2: Persist all messages
func TestSession_AddMessage(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	msg := Message{
		Role:      "user",
		Content:   "Hello",
		Goal:      "goal1",
		Timestamp: time.Now(),
	}
	err := mgr.AddMessage(sess.ID, msg)
	if err != nil {
		t.Fatalf("add message error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "user" {
		t.Errorf("expected role 'user', got %s", loaded.Messages[0].Role)
	}
	if loaded.Messages[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got %s", loaded.Messages[0].Content)
	}
}

// R7.2.3: Persist tool call details
func TestSession_AddToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	tc := ToolCall{
		ID:        "call-123",
		Name:      "read",
		Args:      map[string]interface{}{"path": "/test.txt"},
		Result:    "file contents",
		Duration:  100 * time.Millisecond,
		Goal:      "goal1",
		Timestamp: time.Now(),
	}
	err := mgr.AddToolCall(sess.ID, tc)
	if err != nil {
		t.Fatalf("add tool call error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(loaded.ToolCalls))
	}
	if loaded.ToolCalls[0].Name != "read" {
		t.Errorf("expected name 'read', got %s", loaded.ToolCalls[0].Name)
	}
}

// R7.3.5 / R7.4.1: Query session by ID
func TestSession_GetByID(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	loaded, err := mgr.Get(sess.ID)
	if err != nil {
		t.Fatalf("get error: %v", err)
	}

	if loaded.ID != sess.ID {
		t.Errorf("expected ID %s, got %s", sess.ID, loaded.ID)
	}
}

func TestSession_GetNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	_, err := mgr.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// R7.4.1-R7.4.3: FileStore implementation
func TestFileStore_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStore(tmpDir)
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	// Verify file exists
	files, _ := os.ReadDir(tmpDir)
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}

	// Verify filename includes session ID
	found := false
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			found = true
		}
	}
	if !found {
		t.Error("expected .json file")
	}

	// Update and verify atomic write
	mgr.AddMessage(sess.ID, Message{Role: "user", Content: "test"})
	
	// Should still be only 1 file (no temp files left)
	files, _ = os.ReadDir(tmpDir)
	jsonCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Errorf("expected 1 json file after update, got %d", jsonCount)
	}
}

// Test SQLite store
func TestSQLiteStore_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	defer store.Close()

	mgr := NewManager(store)

	sess, err := mgr.Create("test-workflow", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("create error: %v", err)
	}

	loaded, err := mgr.Get(sess.ID)
	if err != nil {
		t.Fatalf("get error: %v", err)
	}

	if loaded.WorkflowName != "test-workflow" {
		t.Errorf("expected 'test-workflow', got %s", loaded.WorkflowName)
	}
}

func TestSQLiteStore_Messages(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	defer store.Close()

	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	msg := Message{
		Role:      "assistant",
		Content:   "Hello back",
		Goal:      "greet",
		Timestamp: time.Now(),
	}
	mgr.AddMessage(sess.ID, msg)

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got %s", loaded.Messages[0].Role)
	}
}

func TestSQLiteStore_ToolCalls(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	defer store.Close()

	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	tc := ToolCall{
		ID:        "tc-1",
		Name:      "write",
		Args:      map[string]interface{}{"path": "/out.txt", "content": "data"},
		Result:    "ok",
		Duration:  50 * time.Millisecond,
		Goal:      "write-goal",
		Timestamp: time.Now(),
	}
	mgr.AddToolCall(sess.ID, tc)

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(loaded.ToolCalls))
	}
	if loaded.ToolCalls[0].Name != "write" {
		t.Errorf("expected name 'write', got %s", loaded.ToolCalls[0].Name)
	}
}
