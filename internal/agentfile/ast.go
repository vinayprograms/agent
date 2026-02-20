package agentfile

// Node is the interface implemented by all AST nodes.
type Node interface {
	node()
}

// Workflow represents the root AST node of an Agentfile.
type Workflow struct {
	Name         string
	BaseDir      string // directory containing the Agentfile
	Supervised   bool   // global supervision enabled (SUPERVISED at top)
	HumanOnly    bool   // global human-only mode (SUPERVISED HUMAN at top)
	SecurityMode string // "default", "paranoid", or "research" (SECURITY directive)
	SecurityScope string // scope description for research mode (e.g., "authorized pentest of lab environment")
	Inputs       []Input
	Agents       []Agent
	Goals        []Goal
	Steps        []Step
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
	FromPath   string          // path to prompt file or skill directory
	Prompt     string          // loaded prompt content (or skill instructions)
	Requires   string          // capability profile name (e.g., "reasoning-heavy", "code-generation")
	Outputs    []string        // structured output field names (after ->)
	IsSkill    bool            // true if loaded from a skill directory
	SkillDir   string          // path to skill directory (if IsSkill)
	Supervision SupervisionMode // inherit/supervised/unsupervised
	HumanOnly  bool            // requires human approval (SUPERVISED HUMAN)
	Line       int
}

func (a *Agent) node() {}

// Goal represents a GOAL or CONVERGE declaration.
type Goal struct {
	Name        string
	Outcome     string          // inline string content
	FromPath    string          // path to outcome file (mutually exclusive with Outcome)
	Outputs     []string        // structured output field names (after ->)
	UsingAgent  []string        // agent names for multi-agent goals
	IsConverge  bool            // true if this is a CONVERGE goal (iterative convergence)
	WithinLimit *int            // max iterations for CONVERGE (nil if variable reference)
	WithinVar   string          // variable name for CONVERGE limit (if not literal)
	Supervision SupervisionMode // inherit/supervised/unsupervised
	HumanOnly   bool            // requires human approval (SUPERVISED HUMAN)
	Line        int
}

func (g *Goal) node() {}

// Step represents a RUN or LOOP step.
type Step struct {
	Type        StepType
	Name        string
	UsingGoals  []string        // goal names to execute
	WithinLimit *int            // max iterations for LOOP (nil if variable reference)
	WithinVar   string          // variable name for LOOP limit (if not literal)
	Supervision SupervisionMode // inherit/supervised/unsupervised
	HumanOnly   bool            // requires human approval (SUPERVISED HUMAN)
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
	return s.Supervision.Bool(wf.Supervised)
}

// RequiresHuman returns true if this step requires human supervision.
func (s *Step) RequiresHuman(wf *Workflow) bool {
	if s.Supervision == SupervisionEnabled {
		return s.HumanOnly
	}
	if s.Supervision == SupervisionInherit && wf.Supervised {
		return wf.HumanOnly || s.HumanOnly
	}
	return false
}

// IsSupervised returns true if this goal should be supervised.
func (g *Goal) IsSupervised(wf *Workflow) bool {
	return g.Supervision.Bool(wf.Supervised)
}

// RequiresHuman returns true if this goal requires human supervision.
func (g *Goal) RequiresHuman(wf *Workflow) bool {
	if g.Supervision == SupervisionEnabled {
		return g.HumanOnly
	}
	if g.Supervision == SupervisionInherit && wf.Supervised {
		return wf.HumanOnly || g.HumanOnly
	}
	return false
}

// IsSupervised returns true if this agent should be supervised.
func (a *Agent) IsSupervised(wf *Workflow) bool {
	return a.Supervision.Bool(wf.Supervised)
}

// RequiresHuman returns true if this agent requires human supervision.
func (a *Agent) RequiresHuman(wf *Workflow) bool {
	if a.Supervision == SupervisionEnabled {
		return a.HumanOnly
	}
	if a.Supervision == SupervisionInherit && wf.Supervised {
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

// HasSupervisedGoals returns true if any goal is marked SUPERVISED.
func (wf *Workflow) HasSupervisedGoals() bool {
	if wf.Supervised {
		return true
	}
	for _, goal := range wf.Goals {
		if goal.Supervision == SupervisionEnabled {
			return true
		}
	}
	for _, step := range wf.Steps {
		if step.Supervision == SupervisionEnabled {
			return true
		}
	}
	for _, agent := range wf.Agents {
		if agent.Supervision == SupervisionEnabled {
			return true
		}
	}
	return false
}
