package main

import (
	"os"
	"testing"
)

func TestIsPackageFile(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{
			name:     "zip file",
			content:  []byte{'P', 'K', 0x03, 0x04, 0x00, 0x00},
			expected: true,
		},
		{
			name:     "text file",
			content:  []byte("WORKFLOW test\nGOAL main\n"),
			expected: false,
		},
		{
			name:     "empty file",
			content:  []byte{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "test-*.bin")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())

			if _, err := f.Write(tt.content); err != nil {
				t.Fatal(err)
			}
			f.Close()

			got := isPackageFile(f.Name())
			if got != tt.expected {
				t.Errorf("isPackageFile() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsPackageFile_NonExistent(t *testing.T) {
	if isPackageFile("/nonexistent/path/file.zip") {
		t.Error("expected false for non-existent file")
	}
}
