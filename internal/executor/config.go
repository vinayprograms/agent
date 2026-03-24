package executor

import (
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
	"github.com/vinayprograms/agentkit/tools"

	"github.com/vinayprograms/agent/internal/agentfile"
)

// Config contains all dependencies for creating an Executor.
// Required fields: Workflow, and either Provider or ProviderFactory, plus Registry and Policy.
// All other fields are optional.
type Config struct {
	// Core (required)
	Workflow        *agentfile.Workflow
	Provider        llm.Provider
	ProviderFactory llm.ProviderFactory
	Registry        *tools.Registry
	Policy          *policy.Policy

	// Debug mode — when true, logs full content (prompts, responses, tool outputs).
	Debug bool

	// MCP
	MCPManager *mcp.Manager

	// Skills
	SkillRefs []skills.SkillRef

	// Session
	Session           *session.Session
	SessionManager    session.SessionManager
	PersistentSession bool

	// Supervision
	CheckpointStore checkpoint.CheckpointStore
	Supervisor      supervision.Supervisor
	HumanAvailable  bool
	HumanInputChan  chan string

	// Security
	SecurityVerifier      *security.Verifier
	SecurityResearchScope string

	// Timeouts for network operations (seconds). Zero means use default.
	TimeoutMCP       int
	TimeoutWebSearch int
	TimeoutWebFetch  int

	// Observation extraction for semantic memory
	ObservationExtractor ObservationExtractor
	ObservationStore     ObservationStore

	// Metrics collector for heartbeat reporting (optional, used by serve mode)
	MetricsCollector MetricsCollector

	// Swarm collaboration (nil = non-swarm mode)
	InterruptBuffer  *InterruptBuffer
	DiscussPublisher func(goalName, content string)

	// Workspace context injected into system prompt so the agent
	// knows the project layout without needing to discover it.
	WorkspaceContext string

	// Hooks registry for cross-cutting concerns (logging, telemetry, metrics).
	// If nil, a fresh registry is created.
	Hooks *hooks.Registry
}
