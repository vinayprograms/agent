package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/packaging"
)

// runInspectWorkflow shows the structure of an Agentfile.
func runInspectWorkflow(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("%s not found", path)
	}

	wf, err := agentfile.LoadFile(path)
	if err != nil {
		return err
	}

	printWorkflowInfo(wf)
	return nil
}

// runInspectPackage shows the manifest of a package.
func runInspectPackage(path string) error {
	pkg, err := packaging.Load(path)
	if err != nil {
		return fmt.Errorf("loading package: %w", err)
	}

	printPackageInfo(pkg)
	return nil
}

func printWorkflowInfo(wf *agentfile.Workflow) {
	fmt.Printf("Workflow: %s\n\n", wf.Name)

	if len(wf.Inputs) > 0 {
		fmt.Println("Inputs:")
		for _, input := range wf.Inputs {
			if input.Default != nil {
				fmt.Printf("  - %s (default: %s)\n", input.Name, *input.Default)
			} else {
				fmt.Printf("  - %s (required)\n", input.Name)
			}
		}
		fmt.Println()
	}

	if len(wf.Agents) > 0 {
		fmt.Println("Agents:")
		for _, agent := range wf.Agents {
			fmt.Printf("  - %s", agent.Name)
			if agent.FromPath != "" {
				fmt.Printf(" (from %s)", agent.FromPath)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(wf.Goals) > 0 {
		fmt.Println("Goals:")
		for _, goal := range wf.Goals {
			fmt.Printf("  - %s", goal.Name)
			if len(goal.UsingAgent) > 0 {
				fmt.Printf(" [using: %s]", strings.Join(goal.UsingAgent, ", "))
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(wf.Steps) > 0 {
		fmt.Println("Steps:")
		for _, step := range wf.Steps {
			printStep(step)
		}
	}
}

func printStep(step agentfile.Step) {
	switch step.Type {
	case agentfile.StepRUN:
		fmt.Printf("  RUN %s: %s\n", step.Name, strings.Join(step.UsingGoals, ", "))
	case agentfile.StepLOOP:
		limit := "∞"
		if step.WithinLimit != nil {
			limit = fmt.Sprintf("%d", *step.WithinLimit)
		}
		fmt.Printf("  LOOP %s: %s (max %s)\n", step.Name, strings.Join(step.UsingGoals, ", "), limit)
	}
}

func printPackageInfo(pkg *packaging.Package) {
	m := pkg.Manifest
	fmt.Printf("Package: %s@%s\n", m.Name, m.Version)
	if m.Description != "" {
		fmt.Printf("Description: %s\n", m.Description)
	}
	printPackageAuthor(m)
	if m.License != "" {
		fmt.Printf("License: %s\n", m.License)
	}
	fmt.Println()

	printPackageInputs(m)
	printPackageOutputs(m)
	printPackageRequires(m)
	printPackageDependencies(m)

	fmt.Printf("Created: %s\n", m.CreatedAt)
	if pkg.Signature != nil {
		fmt.Printf("Signed: yes (%d bytes)\n", len(pkg.Signature))
	} else {
		fmt.Println("Signed: no")
	}
}

func printPackageAuthor(m *packaging.Manifest) {
	if m.Author == nil {
		return
	}
	if m.Author.Email != "" {
		fmt.Printf("Author: %s <%s>\n", m.Author.Name, m.Author.Email)
	} else if m.Author.Name != "" {
		fmt.Printf("Author: %s\n", m.Author.Name)
	}
	if m.Author.KeyFingerprint != "" {
		fmt.Printf("Key fingerprint: %s\n", m.Author.KeyFingerprint)
	}
}

func printPackageInputs(m *packaging.Manifest) {
	if len(m.Inputs) == 0 {
		return
	}
	fmt.Println("Inputs:")
	for name, input := range m.Inputs {
		if input.Required {
			fmt.Printf("  - %s (required)", name)
		} else {
			fmt.Printf("  - %s (default: %s)", name, input.Default)
		}
		if input.Description != "" {
			fmt.Printf(" - %s", input.Description)
		}
		fmt.Println()
	}
	fmt.Println()
}

func printPackageOutputs(m *packaging.Manifest) {
	if len(m.Outputs) == 0 {
		return
	}
	fmt.Println("Outputs:")
	for name, output := range m.Outputs {
		fmt.Printf("  - %s", name)
		if output.Description != "" {
			fmt.Printf(": %s", output.Description)
		}
		fmt.Println()
	}
	fmt.Println()
}

func printPackageRequires(m *packaging.Manifest) {
	if m.Requires == nil {
		return
	}
	if len(m.Requires.Profiles) > 0 {
		fmt.Printf("Required profiles: %s\n", strings.Join(m.Requires.Profiles, ", "))
	}
	if len(m.Requires.Tools) > 0 {
		fmt.Printf("Required tools: %s\n", strings.Join(m.Requires.Tools, ", "))
	}
	fmt.Println()
}

func printPackageDependencies(m *packaging.Manifest) {
	if len(m.Dependencies) == 0 {
		return
	}
	fmt.Println("Dependencies:")
	for name, version := range m.Dependencies {
		fmt.Printf("  - %s %s\n", name, version)
	}
	fmt.Println()
}
