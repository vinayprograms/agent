package agentfile

// Node is the interface implemented by all AST nodes.
type Node interface {
	node()
}

// Workflow represents the root AST node of an Agentfile.
type Workflow struct {
	Name       string
	BaseDir    string // directory containing the Agentfile
	Supervised bool   // global supervision enabled (SUPERVISED at top)
	HumanOnly  bool   // global human-only mode (SUPERVISED HUMAN at top)
	Inputs     []Input
	Agents     []Agent
	Goals      []Goal
	Steps      []Step
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
	Name       string
	FromPath   string   // path to prompt file or skill directory
	Prompt     string   // loaded prompt content (or skill instructions)
	Requires   string   // capability profile name (e.g., "reasoning-heavy", "code-generation")
	Outputs    []string // structured output field names (after ->)
	IsSkill    bool     // true if loaded from a skill directory
	SkillDir   string   // path to skill directory (if IsSkill)
	Supervised *bool    // nil = inherit, true = supervised, false = unsupervised
	HumanOnly  bool     // requires human approval (SUPERVISED HUMAN)
	Line       int
}

func (a *Agent) node() {}

// Goal represents a GOAL declaration.
type Goal struct {
	Name         string
	Outcome      string   // inline string content
	FromPath     string   // path to outcome file (mutually exclusive with Outcome)
	Outputs      []string // structured output field names (after ->)
	UsingAgent   []string // agent names for multi-agent goals
	Supervised   *bool    // nil = inherit, true = supervised, false = unsupervised
	HumanOnly    bool     // requires human approval (SUPERVISED HUMAN)
	Line         int
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
	Supervised  *bool    // nil = inherit, true = supervised, false = unsupervised
	HumanOnly   bool     // requires human approval (SUPERVISED HUMAN)
	Line        int
}

func (s *Step) node() {}

// IsLoop returns true if this is a LOOP step.
func (s *Step) IsLoop() bool {
	return s.Type == StepLOOP
}

// IsSupervised returns true if this step should be supervised.
// Checks step-level override first, then falls back to workflow default.
func (s *Step) IsSupervised(wf *Workflow) bool {
	if s.Supervised != nil {
		return *s.Supervised
	}
	return wf.Supervised
}

// RequiresHuman returns true if this step requires human supervision.
func (s *Step) RequiresHuman(wf *Workflow) bool {
	if s.Supervised != nil && *s.Supervised {
		return s.HumanOnly
	}
	if wf.Supervised {
		return wf.HumanOnly || s.HumanOnly
	}
	return false
}

// IsSupervised returns true if this goal should be supervised.
func (g *Goal) IsSupervised(wf *Workflow) bool {
	if g.Supervised != nil {
		return *g.Supervised
	}
	return wf.Supervised
}

// RequiresHuman returns true if this goal requires human supervision.
func (g *Goal) RequiresHuman(wf *Workflow) bool {
	if g.Supervised != nil && *g.Supervised {
		return g.HumanOnly
	}
	if wf.Supervised {
		return wf.HumanOnly || g.HumanOnly
	}
	return false
}

// IsSupervised returns true if this agent should be supervised.
func (a *Agent) IsSupervised(wf *Workflow) bool {
	if a.Supervised != nil {
		return *a.Supervised
	}
	return wf.Supervised
}

// RequiresHuman returns true if this agent requires human supervision.
func (a *Agent) RequiresHuman(wf *Workflow) bool {
	if a.Supervised != nil && *a.Supervised {
		return a.HumanOnly
	}
	if wf.Supervised {
		return wf.HumanOnly || a.HumanOnly
	}
	return false
}

// HasHumanRequiredSteps returns true if any step requires human supervision.
func (wf *Workflow) HasHumanRequiredSteps() bool {
	if wf.Supervised && wf.HumanOnly {
		return true
	}
	for _, step := range wf.Steps {
		if step.RequiresHuman(wf) {
			return true
		}
	}
	for _, goal := range wf.Goals {
		if goal.RequiresHuman(wf) {
			return true
		}
	}
	for _, agent := range wf.Agents {
		if agent.RequiresHuman(wf) {
			return true
		}
	}
	return false
}

// GetHumanRequiredStepNames returns names of steps that require human supervision.
func (wf *Workflow) GetHumanRequiredStepNames() []string {
	var names []string
	for _, step := range wf.Steps {
		if step.RequiresHuman(wf) {
			names = append(names, step.Name)
		}
	}
	for _, goal := range wf.Goals {
		if goal.RequiresHuman(wf) {
			names = append(names, goal.Name)
		}
	}
	return names
}
