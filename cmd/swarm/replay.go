package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// replayTUI shows a TUI timeline view of a task execution.
type replayTUI struct {
	taskID string
	events []replayEvent
}

type replayEvent struct {
	Time    time.Time
	Agent   string
	Type    string // "goal", "tool", "result"
	Message string
	Status  string // "success", "failed", "running"
}

func (r *replayTUI) View() string {
	var b strings.Builder

	// Group by agent
	agents := map[string][]replayEvent{}
	for _, e := range r.events {
		agents[e.Agent] = append(agents[e.Agent], e)
	}

	for agent, events := range agents {
		b.WriteString(fmt.Sprintf("─── %s ───────────────────────\n", agent))
		for _, e := range events {
			status := ""
			switch e.Status {
			case "success":
				status = "✓"
			case "failed":
				status = "✗"
			case "running":
				status = "⏳"
			}
			timeStr := e.Time.Format("15:04:05")
			b.WriteString(fmt.Sprintf("  %s %s %s: %s\n", timeStr, status, e.Type, e.Message))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func replayTask(taskID string) error {
	// Load task result
	home, _ := os.UserHomeDir()
	resultPath := filepath.Join(home, ".local", "share", "swarm", "tasks", taskID+".json")

	data, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	var result struct {
		TaskID     string      `json:"task_id"`
		Status     string      `json:"status"`
		Outputs    interface{} `json:"outputs"`
		Error      string      `json:"error"`
		DurationMs int64       `json:"duration_ms"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse result: %w", err)
	}

	fmt.Printf("Task: %s\n", taskID)
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Duration: %dms\n", result.DurationMs)
	if result.Error != "" {
		fmt.Printf("Error: %s\n", result.Error)
	}
	if result.Outputs != nil {
		fmt.Println("\nOutputs:")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result.Outputs)
	}

	return nil
}

func replayWeb(taskID string) error {
	home, _ := os.UserHomeDir()
	resultPath := filepath.Join(home, ".local", "share", "swarm", "tasks", taskID+".json")

	data, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse result: %w", err)
	}

	// Generate HTML
	tmpl := `<!DOCTYPE html>
<html>
<head>
	<title>Task {{.TaskID}}</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 40px auto; max-width: 800px; }
		.header { background: #1a1a2e; color: white; padding: 20px; border-radius: 8px; margin-bottom: 20px; }
		.meta { display: grid; grid-template-columns: auto 1fr; gap: 8px; }
		.label { color: #888; }
		.success { color: #22c55e; }
		.failed { color: #ef4444; }
		pre { background: #f4f4f5; padding: 16px; border-radius: 8px; overflow-x: auto; }
		.timeline { border-left: 2px solid #e5e7eb; padding-left: 20px; }
		.event { margin: 16px 0; }
		.event-time { color: #888; font-size: 0.875rem; }
		.event-type { font-weight: 600; }
		.tool { color: #3b82f6; }
		.goal { color: #8b5cf6; }
	</style>
</head>
<body>
	<div class="header">
		<h1>Task {{.TaskID}}</h1>
		<div class="meta">
			<span class="label">Status:</span>
			<span class="{{.StatusClass}}">{{.Status}}</span>
			<span class="label">Duration:</span>
			<span>{{.DurationMs}}ms</span>
		</div>
	</div>

	{{if .Error}}
	<div class="error">
		<h2>Error</h2>
		<pre>{{.Error}}</pre>
	</div>
	{{end}}

	{{if .Outputs}}
	<div class="outputs">
		<h2>Outputs</h2>
		<pre>{{.OutputsJSON}}</pre>
	</div>
	{{end}}
</body>
</html>`

	t, err := template.New("replay").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	outputsJSON := ""
	if result["outputs"] != nil {
		b, _ := json.MarshalIndent(result["outputs"], "", "  ")
		outputsJSON = string(b)
	}

	statusClass := "success"
	status := fmt.Sprintf("%v", result["status"])
	if status == "failed" {
		statusClass = "failed"
	}

	viewData := struct {
		TaskID      string
		Status      string
		StatusClass string
		DurationMs  int64
		Error       string
		Outputs     interface{}
		OutputsJSON string
	}{
		TaskID:      fmt.Sprintf("%v", result["task_id"]),
		Status:      status,
		StatusClass: statusClass,
		DurationMs:  result["duration_ms"].(int64),
		Error:       fmt.Sprintf("%v", result["error"]),
		Outputs:     result["outputs"],
		OutputsJSON: outputsJSON,
	}

	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("swarm-replay-%s.html", taskID))
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if err := t.Execute(f, viewData); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	fmt.Printf("Generated: %s\n", outPath)

	// Open in browser if possible
	if os.Getenv("DISPLAY") != "" || os.Getenv("BROWSER") != "" {
		browser := os.Getenv("BROWSER")
		if browser == "" {
			browser = "xdg-open"
		}
		exec.Command(browser, outPath).Start()
		fmt.Println("Opened in browser")
	}

	return nil
}
