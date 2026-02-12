// agentmem - Memory investigation tool for headless-agent
//
// Commands:
//   agentmem list [--category=finding|insight|lesson] [--limit=N] <storage-path>
//   agentmem search <query> [--limit=N] <storage-path>
//   agentmem stats <storage-path>
//   agentmem graph [--term=X] <storage-path>
//   agentmem scratchpad <storage-path>
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agentkit/memory"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "list":
		cmdList(args)
	case "search":
		cmdSearch(args)
	case "stats":
		cmdStats(args)
	case "graph":
		cmdGraph(args)
	case "scratchpad":
		cmdScratchpad(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`agentmem - Memory investigation tool for headless-agent

Usage:
  agentmem <command> [options] <storage-path>

Commands:
  list       List all stored observations
  search     Search observations by query
  stats      Show memory statistics
  graph      Inspect semantic graph
  scratchpad Dump scratchpad (key-value store)

Examples:
  agentmem list ./storage
  agentmem list --category=finding --limit=10 ./storage
  agentmem search "database choice" ./storage
  agentmem stats ./storage
  agentmem graph --term=api ./storage
  agentmem scratchpad ./storage`)
}

// cmdList lists all observations, optionally filtered by category
func cmdList(args []string) {
	var category string
	var limit int = 100
	var storagePath string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--category=") {
			category = strings.TrimPrefix(arg, "--category=")
		} else if strings.HasPrefix(arg, "--limit=") {
			fmt.Sscanf(strings.TrimPrefix(arg, "--limit="), "%d", &limit)
		} else if !strings.HasPrefix(arg, "-") {
			storagePath = arg
		}
	}

	if storagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: storage path required")
		os.Exit(1)
	}

	store, err := openStore(storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx := context.Background()

	// Use ListAll for proper enumeration
	items, err := store.ListAll(ctx, category, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing observations: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Println("No observations found.")
		return
	}

	// Group by category for display
	grouped := make(map[string][]memory.ObservationItem)
	for _, item := range items {
		grouped[item.Category] = append(grouped[item.Category], item)
	}

	for _, cat := range []string{"finding", "insight", "lesson"} {
		if items, ok := grouped[cat]; ok && len(items) > 0 {
			fmt.Printf("\n=== %ss (%d) ===\n", strings.ToUpper(cat[:1])+cat[1:], len(items))
			for i, item := range items {
				fmt.Printf("%d. [%s] %s\n", i+1, item.ID[:8], item.Content)
			}
		}
	}
}

// cmdSearch searches observations
func cmdSearch(args []string) {
	var limit int = 10
	var query string
	var storagePath string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--limit=") {
			fmt.Sscanf(strings.TrimPrefix(arg, "--limit="), "%d", &limit)
		} else if !strings.HasPrefix(arg, "-") {
			if query == "" {
				query = arg
			} else {
				storagePath = arg
			}
		}
	}

	if query == "" || storagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: query and storage path required")
		fmt.Fprintln(os.Stderr, "Usage: agentmem search <query> <storage-path>")
		os.Exit(1)
	}

	store, err := openStore(storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx := context.Background()

	results, err := store.RecallFIL(ctx, query, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error searching: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Search: %q\n", query)
	fmt.Println()

	if len(results.Findings) > 0 {
		fmt.Println("=== Findings ===")
		for i, f := range results.Findings {
			fmt.Printf("%d. %s\n", i+1, f)
		}
		fmt.Println()
	}

	if len(results.Insights) > 0 {
		fmt.Println("=== Insights ===")
		for i, f := range results.Insights {
			fmt.Printf("%d. %s\n", i+1, f)
		}
		fmt.Println()
	}

	if len(results.Lessons) > 0 {
		fmt.Println("=== Lessons ===")
		for i, f := range results.Lessons {
			fmt.Printf("%d. %s\n", i+1, f)
		}
		fmt.Println()
	}

	total := len(results.Findings) + len(results.Insights) + len(results.Lessons)
	if total == 0 {
		fmt.Println("No results found.")
	}
}

// cmdStats shows memory statistics
func cmdStats(args []string) {
	var storagePath string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			storagePath = arg
			break
		}
	}

	if storagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: storage path required")
		os.Exit(1)
	}

	// Check what files exist
	fmt.Printf("Storage path: %s\n\n", storagePath)

	// Bleve index
	blevePath := filepath.Join(storagePath, "observations.bleve")
	if info, err := os.Stat(blevePath); err == nil {
		fmt.Printf("📊 Bleve index: %s (exists)\n", blevePath)
		if info.IsDir() {
			var size int64
			filepath.Walk(blevePath, func(_ string, info os.FileInfo, _ error) error {
				if !info.IsDir() {
					size += info.Size()
				}
				return nil
			})
			fmt.Printf("   Size: %s\n", formatBytes(size))
		}
	} else {
		fmt.Printf("📊 Bleve index: not found\n")
	}

	// Semantic graph
	graphPath := filepath.Join(storagePath, "semantic_graph.json")
	if data, err := os.ReadFile(graphPath); err == nil {
		var graph struct {
			Terms map[string]interface{} `json:"terms"`
		}
		if json.Unmarshal(data, &graph) == nil {
			fmt.Printf("🕸️  Semantic graph: %d terms\n", len(graph.Terms))
		}
	} else {
		fmt.Printf("🕸️  Semantic graph: not found\n")
	}

	// KV store
	kvPath := filepath.Join(storagePath, "kv.json")
	if data, err := os.ReadFile(kvPath); err == nil {
		var kv map[string]string
		if json.Unmarshal(data, &kv) == nil {
			fmt.Printf("📝 Scratchpad: %d keys\n", len(kv))
		}
	} else {
		fmt.Printf("📝 Scratchpad: not found\n")
	}

	// Try to get observation counts
	store, err := openStore(storagePath)
	if err == nil {
		defer store.Close()
		ctx := context.Background()

		fmt.Println("\n--- Observation Counts ---")
		for _, cat := range []string{"finding", "insight", "lesson"} {
			items, _ := store.ListAll(ctx, cat, 10000)
			fmt.Printf("  %ss: %d\n", strings.Title(cat), len(items))
		}
	}
}

// cmdGraph inspects the semantic graph
func cmdGraph(args []string) {
	var term string
	var storagePath string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--term=") {
			term = strings.TrimPrefix(arg, "--term=")
		} else if !strings.HasPrefix(arg, "-") {
			storagePath = arg
		}
	}

	if storagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: storage path required")
		os.Exit(1)
	}

	graphPath := filepath.Join(storagePath, "semantic_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading graph: %v\n", err)
		os.Exit(1)
	}

	var graph struct {
		Terms    map[string]json.RawMessage `json:"terms"`
		Provider string                     `json:"provider"`
		Model    string                     `json:"model"`
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing graph: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Semantic Graph\n")
	fmt.Printf("Provider: %s\n", graph.Provider)
	fmt.Printf("Model: %s\n", graph.Model)
	fmt.Printf("Terms: %d\n\n", len(graph.Terms))

	if term != "" {
		// Show specific term
		if termData, ok := graph.Terms[term]; ok {
			var td struct {
				Related []string `json:"related"`
			}
			json.Unmarshal(termData, &td)
			fmt.Printf("Term: %q\n", term)
			fmt.Printf("Related: %v\n", td.Related)
		} else {
			fmt.Printf("Term %q not found in graph\n", term)
		}
	} else {
		// List all terms (first 50)
		count := 0
		for t := range graph.Terms {
			if count >= 50 {
				fmt.Printf("... and %d more\n", len(graph.Terms)-50)
				break
			}
			fmt.Printf("  %s\n", t)
			count++
		}
	}
}

// cmdScratchpad dumps the scratchpad
func cmdScratchpad(args []string) {
	var storagePath string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			storagePath = arg
			break
		}
	}

	if storagePath == "" {
		fmt.Fprintln(os.Stderr, "Error: storage path required")
		os.Exit(1)
	}

	kvPath := filepath.Join(storagePath, "kv.json")
	data, err := os.ReadFile(kvPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading scratchpad: %v\n", err)
		os.Exit(1)
	}

	var kv map[string]string
	if err := json.Unmarshal(data, &kv); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing scratchpad: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Scratchpad (%d keys)\n\n", len(kv))
	for k, v := range kv {
		// Truncate long values
		display := v
		if len(display) > 100 {
			display = display[:100] + "..."
		}
		fmt.Printf("%s = %s\n", k, display)
	}
}

// openStore opens a BleveStore at the given path
func openStore(storagePath string) (*memory.BleveStore, error) {
	return memory.NewBleveStore(memory.BleveStoreConfig{
		BasePath: storagePath,
		Embedder: nil, // Read-only, no embedder needed
	})
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
