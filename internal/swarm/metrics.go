// Copied VERBATIM from agentkit v0.2.1 (heartbeat/metrics.go) to keep the
// swarm wire format stable: the metadata keys emitted here (tokens_in,
// tokens_out, cache_*_tokens, llm_calls, subagents, sup_*, avg_latency_ms)
// are consumed by the swarm web UI and must stay identical.

package swarm

import (
	"strconv"
	"sync"
	"sync/atomic"
)

// MetricsCollector accumulates LLM and execution metrics for heartbeat reporting.
// All methods are safe for concurrent use.
type MetricsCollector struct {
	tokensIn       atomic.Int64
	tokensOut      atomic.Int64
	cacheCreation  atomic.Int64
	cacheRead      atomic.Int64
	llmCalls       atomic.Int64
	supApproved    atomic.Int64
	supDenied      atomic.Int64
	subagents      atomic.Int64
	totalLatencyMs atomic.Int64 // cumulative for averaging

	mu     sync.Mutex
	sender MetadataSetter
}

// MetadataSetter is what the collector needs from a heartbeat sender;
// *BusSender satisfies it.
type MetadataSetter interface {
	SetMetadata(key, value string)
}

// NewMetricsCollector creates a collector that writes to a heartbeat sender.
// A nil sender makes every Record*/Set* a no-op.
func NewMetricsCollector(sender MetadataSetter) *MetricsCollector {
	return &MetricsCollector{sender: sender}
}

// RecordLLMCall records metrics from a single LLM call.
func (m *MetricsCollector) RecordLLMCall(inputTokens, outputTokens, cacheCreation, cacheRead int, latencyMs int64) {
	m.tokensIn.Add(int64(inputTokens))
	m.tokensOut.Add(int64(outputTokens))
	m.cacheCreation.Add(int64(cacheCreation))
	m.cacheRead.Add(int64(cacheRead))
	m.llmCalls.Add(1)
	m.totalLatencyMs.Add(latencyMs)

	m.flush()
}

// RecordSupervision records a supervision decision.
func (m *MetricsCollector) RecordSupervision(approved bool) {
	if approved {
		m.supApproved.Add(1)
	} else {
		m.supDenied.Add(1)
	}
	m.flush()
}

// SetSubagents updates the active sub-agent count.
func (m *MetricsCollector) SetSubagents(count int) {
	m.subagents.Store(int64(count))
	m.flush()
}

// flush writes current metrics to heartbeat metadata.
func (m *MetricsCollector) flush() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sender == nil {
		return
	}

	set := func(key string, v *atomic.Int64) {
		m.sender.SetMetadata(key, strconv.FormatInt(v.Load(), 10))
	}
	set("tokens_in", &m.tokensIn)
	set("tokens_out", &m.tokensOut)
	set("cache_creation_tokens", &m.cacheCreation)
	set("cache_read_tokens", &m.cacheRead)
	set("llm_calls", &m.llmCalls)
	set("subagents", &m.subagents)
	set("sup_approved", &m.supApproved)
	set("sup_denied", &m.supDenied)

	if calls := m.llmCalls.Load(); calls > 0 {
		m.sender.SetMetadata("avg_latency_ms", strconv.FormatInt(m.totalLatencyMs.Load()/calls, 10))
	}
}
