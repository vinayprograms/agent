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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	sess.Status = StatusComplete
	sess.Result = "result data"
	err = mgr.Update(sess)
	if err != nil {
		t.Fatalf("update error: %v", err)
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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	sess.Status = StatusFailed
	sess.Error = "something went wrong"
	err = mgr.Update(sess)
	if err != nil {
		t.Fatalf("update error: %v", err)
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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	sess.State["goal1"] = "result1"
	sess.State["iteration"] = 5
	err = mgr.Update(sess)
	if err != nil {
		t.Fatalf("update state error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if loaded.State["goal1"] != "result1" {
		t.Errorf("expected goal1='result1', got %v", loaded.State["goal1"])
	}
}

// R7.2.2: Persist events (formerly messages)
func TestSession_AddEvent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	event := Event{
		Type:      EventUser,
		Content:   "Hello",
		Goal:      "goal1",
		Timestamp: time.Now(),
	}
	err = mgr.AddEvent(sess.ID, event)
	if err != nil {
		t.Fatalf("add event error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(loaded.Events))
	}
	if loaded.Events[0].Type != EventUser {
		t.Errorf("expected type 'user', got %s", loaded.Events[0].Type)
	}
	if loaded.Events[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got %s", loaded.Events[0].Content)
	}
}

// R7.2.3: Persist tool call events
func TestSession_AddToolCallEvent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	event := Event{
		Type:       EventToolCall,
		Tool:       "read",
		Args:       map[string]interface{}{"path": "/test.txt"},
		Goal:       "goal1",
		Timestamp:  time.Now(),
	}
	err = mgr.AddEvent(sess.ID, event)
	if err != nil {
		t.Fatalf("add tool call event error: %v", err)
	}

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(loaded.Events))
	}
	if loaded.Events[0].Tool != "read" {
		t.Errorf("expected tool 'read', got %s", loaded.Events[0].Tool)
	}
}

// R7.3.5 / R7.4.1: Query session by ID
func TestSession_GetByID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
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
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	_, err = mgr.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// R7.4.1-R7.4.3: FileStore implementation
func TestFileStore_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
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
	mgr.AddEvent(sess.ID, Event{Type: EventUser, Content: "test", Timestamp: time.Now()})
	
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

// Test sequence ID generation
func TestSession_SequenceIDs(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStore(tmpDir)
	if err != nil {
		t.Fatalf("create store error: %v", err)
	}
	mgr := NewManager(store)

	sess, _ := mgr.Create("workflow", nil)
	
	// Add multiple events
	for i := 0; i < 5; i++ {
		mgr.AddEvent(sess.ID, Event{Type: EventUser, Content: "test", Timestamp: time.Now()})
	}

	loaded, _ := mgr.Get(sess.ID)
	if len(loaded.Events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(loaded.Events))
	}
	
	// Verify sequence IDs are monotonic
	for i := 0; i < len(loaded.Events); i++ {
		if loaded.Events[i].SeqID != uint64(i+1) {
			t.Errorf("event %d: expected seq %d, got %d", i, i+1, loaded.Events[i].SeqID)
		}
	}
}

// Test correlation IDs
func TestSession_CorrelationID(t *testing.T) {
	sess := &Session{}
	
	corr1 := sess.StartCorrelation()
	corr2 := sess.StartCorrelation()
	
	if corr1 == "" {
		t.Error("correlation ID should not be empty")
	}
	if corr1 == corr2 {
		t.Error("correlation IDs should be unique")
	}
}
