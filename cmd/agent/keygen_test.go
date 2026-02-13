package main

import (
	"os"
	"testing"
)

func TestParseKeygenArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		want   string
	}{
		{
			name: "default",
			args: []string{},
			want: "agent-key",
		},
		{
			name: "with output",
			args: []string{"--output", "my-key"},
			want: "my-key",
		},
		{
			name: "short output",
			args: []string{"-o", "my-key"},
			want: "my-key",
		},
		{
			name: "equals syntax",
			args: []string{"--output=my-key"},
			want: "my-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseKeygenArgs(tt.args)
			if got != tt.want {
				t.Errorf("parseKeygenArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckKeyPaths_NotExist(t *testing.T) {
	err := checkKeyPaths("/nonexistent/path.pem", "/nonexistent/path.pub")
	if err != nil {
		t.Errorf("expected no error for non-existent paths, got: %v", err)
	}
}

func TestCheckKeyPaths_Exists(t *testing.T) {
	f, err := os.CreateTemp("", "key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	err = checkKeyPaths(f.Name(), "/nonexistent/path.pub")
	if err == nil {
		t.Error("expected error for existing private key path")
	}
}
