package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agent/internal/swarm"
	"github.com/vinayprograms/agentkit/policy"
)

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `{"description": "test", "capabilities": ["code.go"]}`,
			want:  `{"description": "test", "capabilities": ["code.go"]}`,
		},
		{
			name:  "json fences",
			input: "```json\n{\"description\": \"test\", \"capabilities\": [\"code.go\"]}\n```",
			want:  `{"description": "test", "capabilities": ["code.go"]}`,
		},
		{
			name:  "plain fences",
			input: "```\n{\"description\": \"test\"}\n```",
			want:  `{"description": "test"}`,
		},
		{
			name:  "fences with surrounding whitespace",
			input: "\n```json\n{\"description\": \"test\"}\n```\n",
			want:  `{"description": "test"}`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "backticks in middle are untouched",
			input: `{"code": "use ` + "```" + ` for blocks"}`,
			want:  `{"code": "use ` + "```" + ` for blocks"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownFences() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSwarmCapabilities(t *testing.T) {
	if got := parseSwarmCapabilities(""); got != nil {
		t.Errorf("empty: got %v", got)
	}
	got := parseSwarmCapabilities("develop:3,test,review:x,none:0")
	want := []swarm.WorkerCapability{
		{Name: "develop", Replicas: 3},
		{Name: "test", Replicas: 1},
		{Name: "review", Replicas: 1},
		{Name: "none", Replicas: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestBuildTaskText(t *testing.T) {
	task := &swarm.TaskMessage{TaskID: "t1", Capability: "cap", Inputs: map[string]string{"topic": "go"}}
	got := buildTaskText(task)
	if !strings.Contains(got, "Capability: cap") || !strings.Contains(got, "topic: go") {
		t.Errorf("unexpected text %q", got)
	}
	if got := buildTaskText(&swarm.TaskMessage{TaskID: "only-id"}); got != "only-id" {
		t.Errorf("fallback to id, got %q", got)
	}
}

func TestExtractTaskIDFromSubject(t *testing.T) {
	if got := extractTaskIDFromSubject("work.cap.abc"); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := extractTaskIDFromSubject("work.cap"); got != "" {
		t.Errorf("short subject: got %q", got)
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := truncateStr("hello world", 5); got != "hello..." {
		t.Errorf("got %q", got)
	}
}

func TestExtractCapabilitySchema_JSONShape(t *testing.T) {
	def := "x"
	wf := &agentfile.Workflow{
		Name:   "wf",
		Inputs: []agentfile.Input{{Name: "a"}, {Name: "b", Default: &def}},
		Goals:  []agentfile.Goal{{Name: "g", Outputs: []string{"out"}}},
	}
	schema := extractCapabilitySchema(wf, "cap")
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"cap","description":"Workflow: wf","inputs":[{"name":"a","required":true,"type":"string"},{"name":"b","required":false,"default":"x","type":"string"}],"outputs":[{"name":"out","required":false,"type":"string"}]}`
	if string(data) != want {
		t.Errorf("capability JSON changed:\n got %s\nwant %s", data, want)
	}

	skill := schema.skill()
	if skill.ID != "cap" || skill.Name != "cap" || len(skill.Inputs) != 2 || len(skill.Outputs) != 1 {
		t.Errorf("skill conversion: %+v", skill)
	}
	if skill.Inputs[1].Default != "x" || !skill.Inputs[0].Required {
		t.Errorf("skill params: %+v", skill.Inputs)
	}
}

func TestRegistryEntry(t *testing.T) {
	a := &serviceAgent{
		agentID:    "id-1",
		instanceID: "inst-1",
		agentType:  "worker",
		capability: capabilitySchema{Name: "cap"},
	}
	entry := a.registryEntry()
	if entry.ID != "id-1" || entry.Name != "cap" || entry.Version != version {
		t.Errorf("entry %+v", entry)
	}
	if len(entry.Skills) != 1 || entry.Skills[0].ID != "cap" {
		t.Errorf("skills %+v", entry.Skills)
	}
	if entry.Metadata["instance_id"] != "inst-1" || entry.Metadata["type"] != "worker" || entry.Metadata["version"] != version {
		t.Errorf("metadata %v", entry.Metadata)
	}

	// Capability falls back to the workflow name.
	a.capability = capabilitySchema{}
	a.loaded = &run.Loaded{Workflow: &agentfile.Workflow{Name: "wfname"}}
	if got := a.registryEntry().Skills[0].ID; got != "wfname" {
		t.Errorf("fallback skill id %q", got)
	}
}

func TestEnableTool(t *testing.T) {
	enableTool(nil, "dispatch") // no panic

	pol := policy.New()
	pol.Tools = nil
	enableTool(pol, "dispatch")
	if !pol.IsToolEnabled("dispatch") {
		t.Error("dispatch should be enabled")
	}
	pol.Tools["dispatch"].Deny = []string{"x"}
	enableTool(pol, "dispatch")
	if len(pol.Tools["dispatch"].Deny) != 1 {
		t.Error("existing tool policy must not be replaced")
	}
}

func TestDeferredMetrics(t *testing.T) {
	var d deferredMetrics
	// The zero value drops every metric.
	d.RecordLLMCall(1, 2, 3, 4, 5)
	d.RecordSupervision(true)
	d.SetSubagents(2)

	spy := &metricsSpy{}
	d.set(spy)
	d.RecordLLMCall(1, 2, 3, 4, 5)
	d.RecordSupervision(true)
	d.SetSubagents(2)
	if spy.llm != 1 || spy.supervision != 1 || spy.subagents != 2 {
		t.Errorf("metrics not forwarded: %+v", spy)
	}
}

// metricsSpy counts the metrics forwarded to it.
type metricsSpy struct {
	llm         int
	supervision int
	subagents   int
}

func (m *metricsSpy) RecordLLMCall(_, _, _, _ int, _ int64) { m.llm++ }
func (m *metricsSpy) RecordSupervision(bool)                { m.supervision++ }
func (m *metricsSpy) SetSubagents(n int)                    { m.subagents = n }
