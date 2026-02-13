package main

import (
	"testing"
)

func TestParseVerifyArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantKey string
	}{
		{
			name:    "no key",
			args:    []string{},
			wantKey: "",
		},
		{
			name:    "with key",
			args:    []string{"--key", "pub.pem"},
			wantKey: "pub.pem",
		},
		{
			name:    "with key equals",
			args:    []string{"--key=pub.pem"},
			wantKey: "pub.pem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVerifyArgs(tt.args)
			if got != tt.wantKey {
				t.Errorf("parseVerifyArgs() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}
