package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/packaging"
)

// newInspectCmd shows the structure of a workflow or a package.
func newInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect [path]",
		Short: "Show workflow or package structure",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			path := argOr(args, "Agentfile")
			if isPackageFile(path) {
				return runInspectPackage(out, path)
			}
			return runInspectWorkflow(out, path)
		},
	}
}

// runInspectWorkflow shows the structure of an Agentfile.
func runInspectWorkflow(w io.Writer, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("%s not found", path)
	}

	wf, err := agentfile.LoadFile(path)
	if err != nil {
		return err
	}

	printWorkflowInfo(w, wf)
	return nil
}

// runInspectPackage shows the manifest of a package.
func runInspectPackage(w io.Writer, path string) error {
	pkg, err := packaging.Load(path)
	if err != nil {
		return fmt.Errorf("loading package: %w", err)
	}

	printPackageInfo(w, pkg)
	return nil
}

func printWorkflowInfo(w io.Writer, wf *agentfile.Workflow) {
	fmt.Fprintf(w, "Workflow: %s\n\n", wf.Name)

	if len(wf.Inputs) > 0 {
		fmt.Fprintln(w, "Inputs:")
		for _, input := range wf.Inputs {
			if input.Default != nil {
				fmt.Fprintf(w, "  - %s (default: %s)\n", input.Name, *input.Default)
			} else {
				fmt.Fprintf(w, "  - %s (required)\n", input.Name)
			}
		}
		fmt.Fprintln(w)
	}

	if len(wf.Agents) > 0 {
		fmt.Fprintln(w, "Agents:")
		for _, agent := range wf.Agents {
			fmt.Fprintf(w, "  - %s", agent.Name)
			if agent.FromPath != "" {
				fmt.Fprintf(w, " (from %s)", agent.FromPath)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}

	if len(wf.Goals) > 0 {
		fmt.Fprintln(w, "Goals:")
		for _, goal := range wf.Goals {
			fmt.Fprintf(w, "  - %s", goal.Name)
			if len(goal.UsingAgent) > 0 {
				fmt.Fprintf(w, " [using: %s]", strings.Join(goal.UsingAgent, ", "))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}

	if len(wf.Steps) > 0 {
		fmt.Fprintln(w, "Steps:")
		for _, step := range wf.Steps {
			printStep(w, step)
		}
	}
}

func printStep(w io.Writer, step agentfile.Step) {
	if step.Type == agentfile.StepRUN {
		fmt.Fprintf(w, "  RUN %s: %s\n", step.Name, strings.Join(step.UsingGoals, ", "))
	}
}

func printPackageInfo(w io.Writer, pkg *packaging.Package) {
	m := pkg.Manifest
	fmt.Fprintf(w, "Package: %s@%s\n", m.Name, m.Version)
	if m.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", m.Description)
	}
	printPackageAuthor(w, m)
	if m.License != "" {
		fmt.Fprintf(w, "License: %s\n", m.License)
	}
	fmt.Fprintln(w)

	printPackageInputs(w, m)
	printPackageOutputs(w, m)
	printPackageRequires(w, m)
	printPackageDependencies(w, m)

	fmt.Fprintf(w, "Created: %s\n", m.CreatedAt)
	if pkg.Signature != nil {
		fmt.Fprintf(w, "Signed: yes (%d bytes)\n", len(pkg.Signature))
	} else {
		fmt.Fprintln(w, "Signed: no")
	}
}

func printPackageAuthor(w io.Writer, m *packaging.Manifest) {
	if m.Author == nil {
		return
	}
	if m.Author.Email != "" {
		fmt.Fprintf(w, "Author: %s <%s>\n", m.Author.Name, m.Author.Email)
	} else if m.Author.Name != "" {
		fmt.Fprintf(w, "Author: %s\n", m.Author.Name)
	}
	if m.Author.KeyFingerprint != "" {
		fmt.Fprintf(w, "Key fingerprint: %s\n", m.Author.KeyFingerprint)
	}
}

func printPackageInputs(w io.Writer, m *packaging.Manifest) {
	if len(m.Inputs) == 0 {
		return
	}
	fmt.Fprintln(w, "Inputs:")
	for name, input := range m.Inputs {
		if input.Required {
			fmt.Fprintf(w, "  - %s (required)", name)
		} else {
			fmt.Fprintf(w, "  - %s (default: %s)", name, input.Default)
		}
		if input.Description != "" {
			fmt.Fprintf(w, " - %s", input.Description)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}

func printPackageOutputs(w io.Writer, m *packaging.Manifest) {
	if len(m.Outputs) == 0 {
		return
	}
	fmt.Fprintln(w, "Outputs:")
	for name, output := range m.Outputs {
		fmt.Fprintf(w, "  - %s", name)
		if output.Description != "" {
			fmt.Fprintf(w, ": %s", output.Description)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}

func printPackageRequires(w io.Writer, m *packaging.Manifest) {
	if m.Requires == nil {
		return
	}
	if len(m.Requires.Profiles) > 0 {
		fmt.Fprintf(w, "Required profiles: %s\n", strings.Join(m.Requires.Profiles, ", "))
	}
	if len(m.Requires.Tools) > 0 {
		fmt.Fprintf(w, "Required tools: %s\n", strings.Join(m.Requires.Tools, ", "))
	}
	fmt.Fprintln(w)
}

func printPackageDependencies(w io.Writer, m *packaging.Manifest) {
	if len(m.Dependencies) == 0 {
		return
	}
	fmt.Fprintln(w, "Dependencies:")
	for name, version := range m.Dependencies {
		fmt.Fprintf(w, "  - %s %s\n", name, version)
	}
	fmt.Fprintln(w)
}
