package packaging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Manifest represents the public API of an agent package.
type Manifest struct {
	Format       int               `json:"format"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description,omitempty"`
	Author       *Author           `json:"author,omitempty"`
	License      string            `json:"license,omitempty"`
	Inputs       map[string]Input  `json:"inputs,omitempty"`
	Outputs      map[string]Output `json:"outputs,omitempty"`
	Requires     *Requirements     `json:"requires,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	CreatedAt    string            `json:"created_at"`
}

// Author represents package author information.
type Author struct {
	Name           string `json:"name,omitempty"`
	Email          string `json:"email,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
}

// Input represents a package input parameter.
type Input struct {
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Output represents a package output.
type Output struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Requirements represents runtime requirements.
type Requirements struct {
	Profiles []string `json:"profiles,omitempty"`
	Tools    []string `json:"tools,omitempty"`
}

// addProfile appends profile unless already present.
func (r *Requirements) addProfile(profile string) {
	if !slices.Contains(r.Profiles, profile) {
		r.Profiles = append(r.Profiles, profile)
	}
}

// loadOrCreateManifest loads manifest.json or creates one from Agentfile.
func loadOrCreateManifest(sourceDir string, opts PackOptions) (*Manifest, error) {
	manifestPath := filepath.Join(sourceDir, ManifestFile)

	var manifest *Manifest

	if data, err := os.ReadFile(manifestPath); err == nil {
		// Load existing manifest
		manifest = &Manifest{}
		if err := json.Unmarshal(data, manifest); err != nil {
			return nil, fmt.Errorf("invalid manifest.json: %w", err)
		}
		// Still extract data from Agentfile to merge (name, version, inputs, requires)
		extracted := &Manifest{Inputs: make(map[string]Input)}
		if err := extractManifestFromAgentfile(sourceDir, extracted); err == nil {
			// Use Agentfile NAME/VERSION if manifest doesn't have them
			if manifest.Name == "" {
				manifest.Name = extracted.Name
			}
			if manifest.Version == "" {
				manifest.Version = extracted.Version
			}
			// Merge inputs (Agentfile inputs fill in missing)
			if manifest.Inputs == nil {
				manifest.Inputs = extracted.Inputs
			} else {
				for name, input := range extracted.Inputs {
					if _, exists := manifest.Inputs[name]; !exists {
						manifest.Inputs[name] = input
					}
				}
			}
			// Merge requires (combine profiles)
			if extracted.Requires != nil && len(extracted.Requires.Profiles) > 0 {
				if manifest.Requires == nil {
					manifest.Requires = &Requirements{}
				}
				for _, profile := range extracted.Requires.Profiles {
					manifest.Requires.addProfile(profile)
				}
			}
		}
	} else {
		// Create from Agentfile
		manifest = &Manifest{
			Inputs:  make(map[string]Input),
			Outputs: make(map[string]Output),
		}

		if err := extractManifestFromAgentfile(sourceDir, manifest); err != nil {
			return nil, err
		}
	}

	manifest.Format = FormatVersion

	// Apply overrides from options
	if opts.Author != nil {
		manifest.Author = opts.Author
	}
	if opts.Description != "" {
		manifest.Description = opts.Description
	}
	if opts.License != "" {
		manifest.License = opts.License
	}

	return manifest, nil
}

// extractManifestFromAgentfile parses Agentfile to populate manifest.
func extractManifestFromAgentfile(sourceDir string, manifest *Manifest) error {
	agentfilePath := filepath.Join(sourceDir, "Agentfile")
	content, err := os.ReadFile(agentfilePath)
	if err != nil {
		return err
	}

	// Simple line-by-line parsing for NAME, INPUT, VERSION
	for line := range strings.Lines(string(content)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "NAME":
			manifest.Name = fields[1]
		case "VERSION":
			manifest.Version = fields[1]
		case "INPUT":
			name := fields[1]
			input := Input{Required: true, Type: "string"}

			for i := 2; i < len(fields); i++ {
				if fields[i] == "DEFAULT" && i+1 < len(fields) {
					input.Required = false
					input.Default = strings.Trim(fields[i+1], "\"")
					i++
				}
			}
			manifest.Inputs[name] = input
		case "AGENT":
			// Extract required profiles
			for i, f := range fields {
				if f == "REQUIRES" && i+1 < len(fields) {
					if manifest.Requires == nil {
						manifest.Requires = &Requirements{}
					}
					manifest.Requires.addProfile(strings.Trim(fields[i+1], "\""))
				}
			}
		}
	}

	if manifest.Name == "" {
		return fmt.Errorf("Agentfile missing NAME")
	}
	if manifest.Version == "" {
		manifest.Version = "0.0.0"
	}

	return nil
}

// validateAgentReferences checks that AGENT FROM doesn't reference .agent packages.
// Packages must be self-contained; inter-package dependencies use manifest.json.
func validateAgentReferences(agentfilePath string) error {
	content, err := os.ReadFile(agentfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Agentfile: %w", err)
	}

	for lineNum, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// Check AGENT ... FROM <path>
		if fields[0] == "AGENT" {
			for i, f := range fields {
				if f == "FROM" && i+1 < len(fields) {
					path := strings.Trim(fields[i+1], "\"'")
					if strings.HasSuffix(path, ".agent") {
						return fmt.Errorf(
							"line %d: AGENT cannot reference .agent packages (%s). "+
								"Packages must be self-contained. Use manifest.json dependencies for inter-package relationships",
							lineNum+1, path,
						)
					}
				}
			}
		}
	}

	return nil
}
