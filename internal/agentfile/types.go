package agentfile

// SupervisionMode represents the supervision state for agents, goals, and steps.
// Uses explicit enum instead of *bool to avoid pointer indirection and temp variables.
type SupervisionMode int

const (
	// SupervisionInherit means inherit from parent (workflow default).
	SupervisionInherit SupervisionMode = iota
	// SupervisionEnabled means explicitly supervised.
	SupervisionEnabled
	// SupervisionDisabled means explicitly unsupervised.
	SupervisionDisabled
)

// IsSet returns true if this is an explicit setting (not inherit).
func (s SupervisionMode) IsSet() bool {
	return s != SupervisionInherit
}

// Bool returns the boolean value, defaulting to the provided fallback for Inherit.
func (s SupervisionMode) Bool(fallback bool) bool {
	switch s {
	case SupervisionEnabled:
		return true
	case SupervisionDisabled:
		return false
	default:
		return fallback
	}
}

// String returns a human-readable representation.
func (s SupervisionMode) String() string {
	switch s {
	case SupervisionEnabled:
		return "supervised"
	case SupervisionDisabled:
		return "unsupervised"
	default:
		return "inherit"
	}
}

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
