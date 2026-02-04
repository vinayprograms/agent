package agentfile

// Node is the interface implemented by all AST nodes.
type Node interface {
	node()
}

// Workflow represents the root AST node of an Agentfile.
type Workflow struct {
	Name    string
	BaseDir string // directory containing the Agentfile
	Inputs  []Input
	Agents  []Agent
	Goals   []Goal
	Steps   []Step
}

func (w *Workflow) node() {}

// Input represents an INPUT declaration.
type Input struct {
	Name    string
	Default *string // nil if no default
	Line    int
}

func (i *Input) node() {}

// Agent represents an AGENT declaration.
type Agent struct {
	Name     string
	FromPath string   // path to prompt file or skill directory
	Prompt   string   // loaded prompt content (or skill instructions)
	Requires string   // capability profile name (e.g., "reasoning-heavy", "code-generation")
	Outputs  []string // structured output field names (after ->)
	IsSkill  bool     // true if loaded from a skill directory
	SkillDir string   // path to skill directory (if IsSkill)
	Line     int
}

func (a *Agent) node() {}

// Goal represents a GOAL declaration.
type Goal struct {
	Name       string
	Outcome    string   // inline string content
	FromPath   string   // path to outcome file (mutually exclusive with Outcome)
	Outputs    []string // structured output field names (after ->)
	UsingAgent []string // agent names for multi-agent goals
	Line       int
}

func (g *Goal) node() {}

// StepType indicates whether a step is RUN or LOOP.
type StepType int

const (
	StepRUN StepType = iota
	StepLOOP
)

func (s StepType) String() string {
	switch s {
	case StepRUN:
		return "RUN"
	case StepLOOP:
		return "LOOP"
	default:
		return "UNKNOWN"
	}
}

// Step represents a RUN or LOOP step.
type Step struct {
	Type        StepType
	Name        string
	UsingGoals  []string // goal names to execute
	WithinLimit *int     // max iterations for LOOP (nil if variable reference)
	WithinVar   string   // variable name for LOOP limit (if not literal)
	Line        int
}

func (s *Step) node() {}

// IsLoop returns true if this is a LOOP step.
func (s *Step) IsLoop() bool {
	return s.Type == StepLOOP
}
