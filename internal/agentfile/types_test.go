package agentfile

import "testing"

func TestSupervisionMode_IsSet(t *testing.T) {
	tests := []struct {
		name string
		mode SupervisionMode
		want bool
	}{
		{"inherit", SupervisionInherit, false},
		{"enabled", SupervisionEnabled, true},
		{"disabled", SupervisionDisabled, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.IsSet(); got != tt.want {
				t.Errorf("%v.IsSet() = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestSupervisionMode_Bool(t *testing.T) {
	tests := []struct {
		name     string
		mode     SupervisionMode
		fallback bool
		want     bool
	}{
		{"enabled ignores fallback true", SupervisionEnabled, true, true},
		{"enabled ignores fallback false", SupervisionEnabled, false, true},
		{"disabled ignores fallback true", SupervisionDisabled, true, false},
		{"disabled ignores fallback false", SupervisionDisabled, false, false},
		{"inherit uses fallback true", SupervisionInherit, true, true},
		{"inherit uses fallback false", SupervisionInherit, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.Bool(tt.fallback); got != tt.want {
				t.Errorf("%v.Bool(%v) = %v, want %v", tt.mode, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestSupervisionMode_String(t *testing.T) {
	tests := []struct {
		mode SupervisionMode
		want string
	}{
		{SupervisionInherit, "inherit"},
		{SupervisionEnabled, "supervised"},
		{SupervisionDisabled, "unsupervised"},
		{SupervisionMode(99), "inherit"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("SupervisionMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestStepType_String(t *testing.T) {
	tests := []struct {
		typ  StepType
		want string
	}{
		{StepRUN, "RUN"},
		{StepType(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("StepType(%d).String() = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}
