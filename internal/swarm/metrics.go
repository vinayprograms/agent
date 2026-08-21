// Copied VERBATIM from agentkit v0.2.1 (heartbeat/metrics.go) to keep the
// swarm wire format stable: the metadata keys emitted here (tokens_in,
// tokens_out, cache_*_tokens, llm_calls, subagents, sup_*, avg_latency_ms)
// are consumed by the swarm web UI and must stay identical.

package swarm

import (
	"fmt"
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
	sender Sender
}

// NewMetricsCollector creates a collector that writes to a heartbeat sender.
func NewMetricsCollector(sender Sender) *MetricsCollector {
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

	m.sender.SetMetadata("tokens_in", fmt.Sprintf("%d", m.tokensIn.Load()))
	m.sender.SetMetadata("tokens_out", fmt.Sprintf("%d", m.tokensOut.Load()))
	m.sender.SetMetadata("cache_creation_tokens", fmt.Sprintf("%d", m.cacheCreation.Load()))
	m.sender.SetMetadata("cache_read_tokens", fmt.Sprintf("%d", m.cacheRead.Load()))
	m.sender.SetMetadata("llm_calls", fmt.Sprintf("%d", m.llmCalls.Load()))
	m.sender.SetMetadata("subagents", fmt.Sprintf("%d", m.subagents.Load()))
	m.sender.SetMetadata("sup_approved", fmt.Sprintf("%d", m.supApproved.Load()))
	m.sender.SetMetadata("sup_denied", fmt.Sprintf("%d", m.supDenied.Load()))

	calls := m.llmCalls.Load()
	if calls > 0 {
		avg := m.totalLatencyMs.Load() / calls
		m.sender.SetMetadata("avg_latency_ms", fmt.Sprintf("%d", avg))
	}
}
