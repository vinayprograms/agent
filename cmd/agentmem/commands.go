package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vinayprograms/agentkit/memory"
)

// openStore opens a BleveStore at the given path.
func openStore(storagePath string) (*memory.BleveStore, error) {
	return memory.NewBleveStore(memory.BleveStoreConfig{
		BasePath: storagePath,
	})
}

// title uppercases the first rune of s (strings.Title is deprecated).
func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// cmdList lists all observations, optionally filtered by category.
func cmdList(cmd *cobra.Command, storagePath, category string, limit int) error {
	out := cmd.OutOrStdout()

	store, err := openStore(storagePath)
	if err != nil {
		return fmt.Errorf("Error opening store: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	items, err := store.ListAll(ctx, category, limit)
	if err != nil {
		return fmt.Errorf("Error listing observations: %w", err)
	}

	if len(items) == 0 {
		fmt.Fprintln(out, "No observations found.")
		return nil
	}

	grouped := make(map[string][]memory.ObservationItem)
	for _, item := range items {
		grouped[item.Category] = append(grouped[item.Category], item)
	}

	for _, cat := range []string{"finding", "insight", "lesson"} {
		if catItems, ok := grouped[cat]; ok && len(catItems) > 0 {
			fmt.Fprintf(out, "\n=== %ss (%d) ===\n", title(cat), len(catItems))
			for i, item := range catItems {
				fmt.Fprintf(out, "%d. [%s] %s\n", i+1, item.ID[:8], item.Content)
			}
		}
	}
	return nil
}

// cmdSearch searches observations.
func cmdSearch(cmd *cobra.Command, query, storagePath string, limit int) error {
	out := cmd.OutOrStdout()

	store, err := openStore(storagePath)
	if err != nil {
		return fmt.Errorf("Error opening store: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	results, err := store.RecallFIL(ctx, query, limit)
	if err != nil {
		return fmt.Errorf("Error searching: %w", err)
	}

	fmt.Fprintf(out, "Search: %q\n", query)
	fmt.Fprintln(out)

	if len(results.Findings) > 0 {
		fmt.Fprintln(out, "=== Findings ===")
		for i, f := range results.Findings {
			fmt.Fprintf(out, "%d. %s\n", i+1, f)
		}
		fmt.Fprintln(out)
	}

	if len(results.Insights) > 0 {
		fmt.Fprintln(out, "=== Insights ===")
		for i, f := range results.Insights {
			fmt.Fprintf(out, "%d. %s\n", i+1, f)
		}
		fmt.Fprintln(out)
	}

	if len(results.Lessons) > 0 {
		fmt.Fprintln(out, "=== Lessons ===")
		for i, f := range results.Lessons {
			fmt.Fprintf(out, "%d. %s\n", i+1, f)
		}
		fmt.Fprintln(out)
	}

	total := len(results.Findings) + len(results.Insights) + len(results.Lessons)
	if total == 0 {
		fmt.Fprintln(out, "No results found.")
	}
	return nil
}

// cmdStats shows memory statistics.
func cmdStats(cmd *cobra.Command, storagePath string) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Storage path: %s\n\n", storagePath)

	blevePath := filepath.Join(storagePath, "observations.bleve")
	if info, err := os.Stat(blevePath); err == nil {
		fmt.Fprintf(out, "📊 Bleve index: %s (exists)\n", blevePath)
		if info.IsDir() {
			size, err := dirSize(blevePath)
			if err != nil {
				return fmt.Errorf("Error reading bleve index: %w", err)
			}
			fmt.Fprintf(out, "   Size: %s\n", formatBytes(size))
		}
	} else {
		fmt.Fprintf(out, "📊 Bleve index: not found\n")
	}

	graphPath := filepath.Join(storagePath, "semantic_graph.json")
	if data, err := os.ReadFile(graphPath); err == nil {
		var graph struct {
			Terms map[string]any `json:"terms"`
		}
		if json.Unmarshal(data, &graph) == nil {
			fmt.Fprintf(out, "🕸️  Semantic graph: %d terms\n", len(graph.Terms))
		}
	} else {
		fmt.Fprintf(out, "🕸️  Semantic graph: not found\n")
	}

	kvPath := filepath.Join(storagePath, "kv.json")
	if data, err := os.ReadFile(kvPath); err == nil {
		var kv map[string]string
		if json.Unmarshal(data, &kv) == nil {
			fmt.Fprintf(out, "📝 Scratchpad: %d keys\n", len(kv))
		}
	} else {
		fmt.Fprintf(out, "📝 Scratchpad: not found\n")
	}

	store, err := openStore(storagePath)
	if err == nil {
		defer store.Close()
		ctx := context.Background()

		fmt.Fprintln(out, "\n--- Observation Counts ---")
		for _, cat := range []string{"finding", "insight", "lesson"} {
			items, _ := store.ListAll(ctx, cat, 10000)
			fmt.Fprintf(out, "  %ss: %d\n", title(cat), len(items))
		}
	}
	return nil
}

// dirSize sums the size of all regular files under root, walking safely
// with WalkDir (unlike filepath.Walk, WalkDir never hands the callback a
// nil FileInfo, so an unreadable entry cannot cause a nil-pointer panic).
func dirSize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

// cmdGraph inspects the semantic graph.
func cmdGraph(cmd *cobra.Command, storagePath, term string) error {
	out := cmd.OutOrStdout()

	graphPath := filepath.Join(storagePath, "semantic_graph.json")
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return fmt.Errorf("Error reading graph: %w", err)
	}

	var graph struct {
		Terms    map[string]json.RawMessage `json:"terms"`
		Provider string                     `json:"provider"`
		Model    string                     `json:"model"`
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		return fmt.Errorf("Error parsing graph: %w", err)
	}

	fmt.Fprintf(out, "Semantic Graph\n")
	fmt.Fprintf(out, "Provider: %s\n", graph.Provider)
	fmt.Fprintf(out, "Model: %s\n", graph.Model)
	fmt.Fprintf(out, "Terms: %d\n\n", len(graph.Terms))

	if term != "" {
		if termData, ok := graph.Terms[term]; ok {
			var td struct {
				Related []string `json:"related"`
			}
			if err := json.Unmarshal(termData, &td); err != nil {
				return fmt.Errorf("Error parsing term %q: %w", term, err)
			}
			fmt.Fprintf(out, "Term: %q\n", term)
			fmt.Fprintf(out, "Related: %v\n", td.Related)
		} else {
			fmt.Fprintf(out, "Term %q not found in graph\n", term)
		}
		return nil
	}

	count := 0
	for t := range graph.Terms {
		if count >= 50 {
			fmt.Fprintf(out, "... and %d more\n", len(graph.Terms)-50)
			break
		}
		fmt.Fprintf(out, "  %s\n", t)
		count++
	}
	return nil
}

// cmdScratchpad dumps the scratchpad.
func cmdScratchpad(cmd *cobra.Command, storagePath string) error {
	out := cmd.OutOrStdout()

	kvPath := filepath.Join(storagePath, "kv.json")
	data, err := os.ReadFile(kvPath)
	if err != nil {
		return fmt.Errorf("Error reading scratchpad: %w", err)
	}

	var kv map[string]string
	if err := json.Unmarshal(data, &kv); err != nil {
		return fmt.Errorf("Error parsing scratchpad: %w", err)
	}

	fmt.Fprintf(out, "Scratchpad (%d keys)\n\n", len(kv))
	for k, v := range kv {
		display := v
		if len(display) > 100 {
			display = display[:100] + "..."
		}
		fmt.Fprintf(out, "%s = %s\n", k, display)
	}
	return nil
}

// formatBytes renders a byte count as a human-readable size.
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
