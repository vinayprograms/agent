package llm

import (
	"testing"
)

func TestInferThinkingLevel(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		tools    []ToolDef
		expected ThinkingLevel
	}{
		{
			name: "simple greeting",
			messages: []Message{
				{Role: "user", Content: "Hello, how are you?"},
			},
			expected: ThinkingOff,
		},
		{
			name: "math problem",
			messages: []Message{
				{Role: "user", Content: "Calculate 2^10 + 15/3"},
			},
			expected: ThinkingHigh,
		},
		{
			name: "prove keyword",
			messages: []Message{
				{Role: "user", Content: "Prove that the sum of angles in a triangle is 180 degrees"},
			},
			expected: ThinkingHigh,
		},
		{
			name: "architecture design",
			messages: []Message{
				{Role: "user", Content: "Design system for a real-time chat application with 1M users"},
			},
			expected: ThinkingHigh,
		},
		{
			name: "security analysis",
			messages: []Message{
				{Role: "user", Content: "Do a security analysis of this authentication flow"},
			},
			expected: ThinkingHigh,
		},
		{
			name: "debug request",
			messages: []Message{
				{Role: "user", Content: "Why is this function returning null?"},
			},
			expected: ThinkingHigh,
		},
		{
			name: "code implementation",
			messages: []Message{
				{Role: "user", Content: "Implement a function to sort an array"},
			},
			expected: ThinkingMedium,
		},
		{
			name: "step by step",
			messages: []Message{
				{Role: "user", Content: "Explain step by step how to deploy this"},
			},
			expected: ThinkingMedium,
		},
		{
			name: "refactor request",
			messages: []Message{
				{Role: "user", Content: "Refactor this code to be more maintainable"},
			},
			expected: ThinkingMedium,
		},
		{
			name: "many tools",
			messages: []Message{
				{Role: "user", Content: "Do something"},
			},
			tools:    make([]ToolDef, 12),
			expected: ThinkingHigh,
		},
		{
			name: "moderate tools",
			messages: []Message{
				{Role: "user", Content: "Do something"},
			},
			tools:    make([]ToolDef, 7),
			expected: ThinkingMedium,
		},
		{
			name: "how to question",
			messages: []Message{
				{Role: "user", Content: "How to install Docker on Ubuntu?"},
			},
			expected: ThinkingLow,
		},
		{
			name: "recommendation request",
			messages: []Message{
				{Role: "user", Content: "What is the best database for this use case?"},
			},
			expected: ThinkingLow,
		},
		{
			name: "long context",
			messages: []Message{
				{Role: "user", Content: string(make([]byte, 3500))},
			},
			expected: ThinkingHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferThinkingLevel(tt.messages, tt.tools)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestResolveThinkingLevel(t *testing.T) {
	messages := []Message{{Role: "user", Content: "Simple hello"}}
	tools := []ToolDef{}

	// Auto mode uses classifier
	config := ThinkingConfig{Level: ThinkingAuto}
	result := ResolveThinkingLevel(config, messages, tools)
	if result != ThinkingOff {
		t.Errorf("auto mode should return Off for simple message, got %s", result)
	}

	// Fixed mode ignores classifier
	config = ThinkingConfig{Level: ThinkingHigh}
	result = ResolveThinkingLevel(config, messages, tools)
	if result != ThinkingHigh {
		t.Errorf("fixed mode should return High, got %s", result)
	}

	// Empty level defaults to auto
	config = ThinkingConfig{Level: ""}
	result = ResolveThinkingLevel(config, messages, tools)
	if result != ThinkingOff {
		t.Errorf("empty level should default to auto, got %s", result)
	}
}

func TestThinkingLevelToAnthropicBudget(t *testing.T) {
	// Config budget takes precedence
	budget := ThinkingLevelToAnthropicBudget(ThinkingHigh, 20000)
	if budget != 20000 {
		t.Errorf("expected config budget 20000, got %d", budget)
	}

	// Default budgets
	budget = ThinkingLevelToAnthropicBudget(ThinkingHigh, 0)
	if budget != 16000 {
		t.Errorf("expected high budget 16000, got %d", budget)
	}

	budget = ThinkingLevelToAnthropicBudget(ThinkingMedium, 0)
	if budget != 8000 {
		t.Errorf("expected medium budget 8000, got %d", budget)
	}

	budget = ThinkingLevelToAnthropicBudget(ThinkingLow, 0)
	if budget != 4000 {
		t.Errorf("expected low budget 4000, got %d", budget)
	}

	budget = ThinkingLevelToAnthropicBudget(ThinkingOff, 0)
	if budget != 0 {
		t.Errorf("expected off budget 0, got %d", budget)
	}
}

func TestContainsMathExpression(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Calculate 2+3", true},
		{"What is 10/2?", true},
		{"Compute 2^10", true},
		{"Hello world", false},
		{"x > y comparison", false},
		{"1/2 fraction", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := containsMathExpression(tt.input)
			if result != tt.expected {
				t.Errorf("containsMathExpression(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
