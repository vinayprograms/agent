package swarm

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestTaskWireTags pins the JSON wire format (agentkit v0.2.1 schema).
func TestTaskWireTags(t *testing.T) {
	want := map[string][]string{
		"TaskMessage": {"task_id", "idempotency_key", "correlation_id", "parent_task_id", "root_task_id",
			"capability", "reply_to", "timeout_seconds", "max_attempts", "attempt", "inputs", "prior_outputs",
			"created_at", "submitted_at", "submitted_by", "tags", "metadata"},
		"TaskResult": {"task_id", "correlation_id", "status", "outputs", "error", "agent_id", "attempt",
			"duration_ms", "completed_at", "metadata"},
		"Heartbeat": {"agent_id", "timestamp", "status", "load", "metadata"},
	}
	types := map[string]reflect.Type{
		"TaskMessage": reflect.TypeOf(TaskMessage{}),
		"TaskResult":  reflect.TypeOf(TaskResult{}),
		"Heartbeat":   reflect.TypeOf(Heartbeat{}),
	}
	for name, typ := range types {
		var got []string
		for i := range typ.NumField() {
			tag := typ.Field(i).Tag.Get("json")
			for j := range tag {
				if tag[j] == ',' {
					tag = tag[:j]
					break
				}
			}
			got = append(got, tag)
		}
		if !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s json tags = %v, want %v", name, got, want[name])
		}
	}
}

func TestTaskMessageValidate(t *testing.T) {
	tests := []struct {
		name string
		msg  TaskMessage
		err  error
	}{
		{"missing id", TaskMessage{Capability: "c"}, ErrInvalidTask},
		{"missing capability", TaskMessage{TaskID: "t"}, ErrInvalidTask},
		{"ok nil inputs", TaskMessage{TaskID: "t", Capability: "c"}, nil},
		{"ok with inputs", TaskMessage{TaskID: "t", Capability: "c", Inputs: map[string]string{"a": "b"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if !errors.Is(err, tt.err) {
				t.Fatalf("Validate() = %v, want %v", err, tt.err)
			}
			if tt.name == "ok nil inputs" && tt.msg.Inputs != nil {
				t.Error("Validate must not mutate the message")
			}
		})
	}
}

func TestTaskMessageRoundTrip(t *testing.T) {
	m := NewTaskMessage("t-1", "develop", map[string]string{"task": "do it"})
	if m.Attempt != 1 || m.CreatedAt.IsZero() {
		t.Fatalf("NewTaskMessage defaults wrong: %+v", m)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["task_id"] != "t-1" || raw["capability"] != "develop" {
		t.Errorf("wire keys wrong: %s", data)
	}
	got, err := UnmarshalTaskMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "t-1" || got.Inputs["task"] != "do it" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if _, err := UnmarshalTaskMessage([]byte("{")); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestTaskResultRoundTrip(t *testing.T) {
	before := time.Now()
	r := NewTaskResult("t-1", "agent-a", ResultFailed)
	if r.Status != ResultFailed || r.CompletedAt.Before(before) {
		t.Fatalf("NewTaskResult defaults wrong: %+v", r)
	}
	r.DurationMs = 42
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["agent_id"] != "agent-a" || raw["duration_ms"] != float64(42) || raw["status"] != "failed" {
		t.Errorf("wire keys wrong: %s", data)
	}
	got, err := UnmarshalTaskResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "agent-a" || got.TaskID != "t-1" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if _, err := UnmarshalTaskResult([]byte("{")); err == nil {
		t.Error("expected error for malformed JSON")
	}
	if ResultSuccess != "success" || ResultTimeout != "timeout" {
		t.Error("status constants changed")
	}
}
