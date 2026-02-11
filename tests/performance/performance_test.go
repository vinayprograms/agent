// Package performance contains performance and benchmark tests.
package performance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// BenchmarkLexer benchmarks the lexer performance.
func BenchmarkLexer(b *testing.B) {
	input := `NAME benchmark-test
INPUT topic DEFAULT "golang"
INPUT max_iterations DEFAULT 10
AGENT creative FROM agents/creative.md
AGENT critic FROM agents/critic.md
GOAL analyze "Analyze $topic thoroughly"
GOAL review "Review the analysis" USING creative, critic
GOAL summarize "Create a summary"
RUN setup USING analyze
LOOP refine USING review WITHIN $max_iterations
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
INPUT max_iterations DEFAULT 10
GOAL analyze "Analyze $topic thoroughly"
GOAL review "Review the analysis"
GOAL summarize "Create a summary"
RUN setup USING analyze
LOOP refine USING review WITHIN $max_iterations
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
	pol.Workspace = "/home/user/project"
	pol.Tools["read"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{"/home/user/project/**"},
		Deny:    []string{"/home/user/project/.git/**", "**/.env"},
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
	pol := policy.New()
	pol.Workspace = b.TempDir()
	registry := tools.NewRegistry(pol)

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

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		exec := executor.NewExecutor(wf, provider, nil, nil)
		exec.Run(context.Background(), nil)
	}
}

// BenchmarkToolExecution benchmarks tool execution overhead.
func BenchmarkToolExecution(b *testing.B) {
	tmpDir := b.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content for benchmark"), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["read"] = &policy.ToolPolicy{Enabled: true, Allow: []string{"**"}}
	registry := tools.NewRegistry(pol)
	readTool := registry.Get("read")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		readTool.Execute(context.Background(), map[string]interface{}{
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

	provider := llm.NewMockProvider()
	provider.SetResponse("Done")

	exec := executor.NewExecutor(wf, provider, nil, nil)
	result, err := exec.Run(context.Background(), nil)
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

// TestPerformance_DeepLoop tests performance with many loop iterations.
func TestPerformance_DeepLoop(t *testing.T) {
	maxIterations := 50
	wf := &agentfile.Workflow{
		Name: "deep-loop",
		Goals: []agentfile.Goal{
			{Name: "iterate", Outcome: "Iterate"},
		},
		Steps: []agentfile.Step{
			{Type: agentfile.StepLOOP, UsingGoals: []string{"iterate"}, WithinLimit: &maxIterations},
		},
	}

	// Provider that simulates tool calls to prevent early convergence
	callCount := 0
	provider := llm.NewMockProvider()
	provider.ChatFunc = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		callCount++
		
		// Check if this is after a tool result
		hasToolResult := false
		for _, msg := range req.Messages {
			if msg.Role == "tool" {
				hasToolResult = true
				break
			}
		}
		
		if hasToolResult {
			// Complete this iteration with unique output
			return &llm.ChatResponse{Content: fmt.Sprintf("Iteration %d complete", callCount)}, nil
		}
		
		// First call in iteration: return a tool call
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCallResponse{
				{ID: fmt.Sprintf("tc-%d", callCount), Name: "ls", Args: map[string]interface{}{"path": "."}},
			},
		}, nil
	}

	tmpDir := t.TempDir()
	pol := policy.New()
	pol.Workspace = tmpDir
	registry := tools.NewRegistry(pol)

	exec := executor.NewExecutor(wf, provider, registry, pol)
	result, err := exec.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.Iterations["iterate"] != maxIterations {
		t.Errorf("expected %d iterations, got %d", maxIterations, result.Iterations["iterate"])
	}
}
