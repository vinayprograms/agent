package main

import "testing"

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `{"description": "test", "capabilities": ["code.go"]}`,
			want:  `{"description": "test", "capabilities": ["code.go"]}`,
		},
		{
			name:  "json fences",
			input: "```json\n{\"description\": \"test\", \"capabilities\": [\"code.go\"]}\n```",
			want:  `{"description": "test", "capabilities": ["code.go"]}`,
		},
		{
			name:  "plain fences",
			input: "```\n{\"description\": \"test\"}\n```",
			want:  `{"description": "test"}`,
		},
		{
			name:  "fences with surrounding whitespace",
			input: "\n```json\n{\"description\": \"test\"}\n```\n",
			want:  `{"description": "test"}`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "backticks in middle are untouched",
			input: `{"code": "use ` + "```" + ` for blocks"}`,
			want:  `{"code": "use ` + "```" + ` for blocks"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownFences() = %q, want %q", got, tt.want)
			}
		})
	}
}
