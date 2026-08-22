// Package performance contains performance and benchmark tests.
package performance

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agent/tests/internal/testkit"
	"github.com/vinayprograms/agentkit/policy"
)

// BenchmarkLexer benchmarks the lexer performance.
func BenchmarkLexer(b *testing.B) {
	input := `NAME benchmark-test
INPUT topic DEFAULT "golang"
AGENT creative FROM agents/creative.md
AGENT critic FROM agents/critic.md
GOAL analyze "Analyze $topic thoroughly"
GOAL review "Review the analysis" USING creative, critic
GOAL summarize "Create a summary"
RUN setup USING analyze, review
RUN finish USING summarize
`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lexer := agentfile.NewLexer(input)
		for {
			tok := lexer.NextToken()
			if tok.Type == agentfile.TokenEOF {
				break
			}
		}
	}
}

// BenchmarkParser benchmarks the parser performance.
func BenchmarkParser(b *testing.B) {
	input := `NAME benchmark-test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic thoroughly"
GOAL review "Review the analysis"
GOAL summarize "Create a summary"
RUN setup USING analyze, review
RUN finish USING summarize
`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := agentfile.ParseString(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPolicyCheck benchmarks policy path checking.
func BenchmarkPolicyCheck(b *testing.B) {
	pol := policy.New()
	pol.Tools["read"] = &policy.ToolPolicy{
		Allow: []string{"/home/user/project/**"},
		Deny:  []string{"/home/user/project/.git/**", "**/.env"},
	}

	paths := []string{
		"/home/user/project/main.go",
		"/home/user/project/internal/pkg/file.go",
		"/home/user/project/.git/config",
		"/home/user/project/secrets/.env",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := paths[i%len(paths)]
		pol.CheckPath("read", path)
	}
}

// BenchmarkToolRegistry benchmarks tool lookup.
func BenchmarkToolRegistry(b *testing.B) {
	registry := testkit.Registry(b, testkit.PermissivePolicy(), b.TempDir())

	toolNames := []string{"read", "write", "edit", "glob", "grep", "ls", "bash"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := toolNames[i%len(toolNames)]
		registry.Get(name)
	}
}

// BenchmarkExecutorSimple benchmarks a simple workflow execution.
func BenchmarkExecutorSimple(b *testing.B) {
	wf := &agentfile.Workflow{
		Name: "benchmark",
		Goals: []agentfile.Goal{
			{Name: "task1", Outcome: "Do task 1"},
			{Name: "task2", Outcome: "Do task 2"},
			{Name: "task3", Outcome: "Do task 3"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepRUN, UsingGoals: []string{"task1", "task2", "task3"}},
		},
	}

	provider := llmmock.New()
	provider.SetResponse("Done")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		exec := testkit.Executor(b, executor.Config{Workflow: wf, Model: provider})
		exec.Run(context.Background(), executor.RunOptions{})
	}
}

// BenchmarkToolExecution benchmarks tool execution overhead.
func BenchmarkToolExecution(b *testing.B) {
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content for benchmark"), 0644)

	pol := policy.New()
	pol.Tools["read"] = &policy.ToolPolicy{Allow: []string{"**"}}
	registry := testkit.Registry(b, pol, tmpDir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Execute(context.Background(), "read", map[string]any{
			"path": testFile,
		})
	}
}

// BenchmarkFileLoadAndParse benchmarks loading and parsing from disk.
func BenchmarkFileLoadAndParse(b *testing.B) {
	tmpDir := b.TempDir()

	content := `NAME benchmark-file-test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic"
GOAL summarize "Summarize results"
RUN main USING analyze, summarize
`
	path := filepath.Join(tmpDir, "Agentfile")
	os.WriteFile(path, []byte(content), 0644)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := agentfile.LoadFile(path)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestPerformance_ManyGoals tests performance with many goals.
func TestPerformance_ManyGoals(t *testing.T) {
	wf := &agentfile.Workflow{Name: "many-goals"}

	// Create 100 goals
	goalNames := make([]string, 100)
	for i := 0; i < 100; i++ {
		name := "goal" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		goalNames[i] = name
		wf.Goals = append(wf.Goals, agentfile.Goal{
			Name:    name,
			Outcome: "Do " + name,
		})
	}
	wf.Steps = append(wf.Steps, agentfile.Step{
		Type:       agentfile.StepRUN,
		UsingGoals: goalNames,
	})

	provider := llmmock.New()
	provider.SetResponse("Done")

	exec := testkit.Executor(t, executor.Config{Workflow: wf, Model: provider})
	result, err := exec.Run(t.Context(), executor.RunOptions{})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.Status != executor.StatusComplete {
		t.Errorf("expected Complete, got %s", result.Status)
	}
	if len(result.Outputs) != 100 {
		t.Errorf("expected 100 outputs, got %d", len(result.Outputs))
	}
}
