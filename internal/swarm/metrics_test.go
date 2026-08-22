package swarm

import (
	"sync"
	"testing"
)

type recordingSender struct {
	mu   sync.Mutex
	meta map[string]string
}

func (r *recordingSender) SetMetadata(k, v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.meta == nil {
		r.meta = map[string]string{}
	}
	r.meta[k] = v
}

func TestMetricsCollector(t *testing.T) {
	rs := &recordingSender{}
	mc := NewMetricsCollector(rs)

	mc.RecordLLMCall(10, 20, 3, 4, 100)
	mc.RecordLLMCall(1, 2, 0, 0, 300)
	mc.RecordSupervision(true)
	mc.RecordSupervision(false)
	mc.RecordSupervision(false)
	mc.SetSubagents(3)

	want := map[string]string{
		"tokens_in":             "11",
		"tokens_out":            "22",
		"cache_creation_tokens": "3",
		"cache_read_tokens":     "4",
		"llm_calls":             "2",
		"subagents":             "3",
		"sup_approved":          "1",
		"sup_denied":            "2",
		"avg_latency_ms":        "200",
	}
	for k, v := range want {
		if rs.meta[k] != v {
			t.Errorf("%s = %q, want %q", k, rs.meta[k], v)
		}
	}
	if len(rs.meta) != len(want) {
		t.Errorf("unexpected extra metadata keys: %v", rs.meta)
	}
}

func TestMetricsCollectorNoLatencyBeforeCalls(t *testing.T) {
	rs := &recordingSender{}
	mc := NewMetricsCollector(rs)
	mc.SetSubagents(1)
	if _, ok := rs.meta["avg_latency_ms"]; ok {
		t.Error("avg_latency_ms must not be set before any LLM call")
	}
}

func TestMetricsCollectorNilSender(t *testing.T) {
	mc := NewMetricsCollector(nil)
	mc.RecordLLMCall(1, 1, 0, 0, 1) // must not panic
	mc.RecordSupervision(true)
	mc.SetSubagents(0)
}
