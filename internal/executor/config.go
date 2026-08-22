package executor

import (
	"log/slog"

	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"

	"github.com/vinayprograms/agent/internal/agentfile"
)

// SecurityMode selects how tool calls are verified against untrusted content.
type SecurityMode string

const (
	// SecurityDefault runs the screener first and escalates to the reviewer
	// only when the screener cannot decide.
	SecurityDefault SecurityMode = "default"
	// SecurityParanoid runs every configured stage; any deny stops the call.
	SecurityParanoid SecurityMode = "paranoid"
	// SecurityResearch is SecurityDefault with Scope passed to the stages as
	// authorized research context (and injected into agent prompts).
	SecurityResearch SecurityMode = "research"
)

// SecurityConfig configures content-trust verification of tool calls.
// The executor builds the contentguard pipeline from it at construction;
// New returns an error if the configuration cannot be honoured (missing
// Reviewer, invalid Patterns) rather than running unverified.
type SecurityConfig struct {
	Mode SecurityMode // empty means SecurityDefault
	// Scope is the authorized research scope (SecurityResearch only).
	Scope string
	// Screener is the cheap triage model. nil disables the screener stage.
	Screener llm.Model
	// Reviewer is the full review model. Required whenever Config.Security
	// is set: it is the stage that resolves escalations, and without it
	// flagged calls would either always be denied or — in paranoid mode —
	// allowed.
	Reviewer llm.Model
	// Patterns are extra "name:regex" injection patterns; Keywords are extra
	// sensitive keywords (from policy [content.security]).
	Patterns []string
	Keywords []string
}

// Config contains all dependencies for creating an Executor.
// Required fields: Workflow, and either Model or Resolver, plus Registry and Policy.
// All other fields are optional.
type Config struct {
	// Core (required)
	Workflow *agentfile.Workflow
	Model    llm.Model    // default model
	Resolver llm.Resolver // profile-based models (sub-agents with REQUIRES)
	Registry *tools.Registry
	Policy   *policy.Policy

	// SpawnBinder is the late-bound spawn_agents tool registered by the
	// runtime; the executor binds its sub-agent spawner to it. nil when the
	// registry has no spawn tool.
	SpawnBinder *tools.SpawnBinder

	// Logger receives structured execution events. nil means slog.Default().
	Logger *slog.Logger

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
	CheckpointStore supervision.Store
	Supervisor      supervision.Supervisor
	HumanAvailable  bool
	HumanInputChan  chan string

	// Security. nil disables tool-call verification entirely; when set,
	// Security.Reviewer must be non-nil or New fails.
	Security *SecurityConfig

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
