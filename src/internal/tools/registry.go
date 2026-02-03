// Package tools provides the tool registry and built-in tools.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openclaw/headless-agent/internal/policy"
)

// Tool represents an executable tool.
type Tool interface {
	// Name returns the tool name.
	Name() string
	// Description returns a description for the LLM.
	Description() string
	// Parameters returns the JSON schema for parameters.
	Parameters() map[string]interface{}
	// Execute runs the tool with the given arguments.
	Execute(ctx context.Context, args map[string]interface{}) (interface{}, error)
}

// ToolDefinition is the LLM-facing tool definition.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// DirEntry represents a directory entry for ls.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ExecResult represents the result of bash execution.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// Registry holds all registered tools.
type Registry struct {
	tools  map[string]Tool
	policy *policy.Policy
}

// NewRegistry creates a new registry with built-in tools.
func NewRegistry(pol *policy.Policy) *Registry {
	r := &Registry{
		tools:  make(map[string]Tool),
		policy: pol,
	}
	r.registerBuiltins()
	return r
}

// registerBuiltins registers all built-in tools.
func (r *Registry) registerBuiltins() {
	r.Register(&readTool{policy: r.policy})
	r.Register(&writeTool{policy: r.policy})
	r.Register(&editTool{policy: r.policy})
	r.Register(&globTool{policy: r.policy})
	r.Register(&grepTool{policy: r.policy})
	r.Register(&lsTool{policy: r.policy})
	r.Register(&bashTool{policy: r.policy})
	r.Register(&webFetchTool{policy: r.policy})
	r.Register(&webSearchTool{policy: r.policy})
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

// Definitions returns LLM-facing definitions for enabled tools.
func (r *Registry) Definitions() []ToolDefinition {
	var defs []ToolDefinition
	for _, t := range r.tools {
		if !r.policy.IsToolEnabled(t.Name()) {
			continue
		}
		defs = append(defs, ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// --- Built-in Tools ---

// readTool implements the read tool (R5.2.1).
type readTool struct {
	policy *policy.Policy
}

func (t *readTool) Name() string { return "read" }

func (t *readTool) Description() string {
	return "Read the contents of a file at the given path."
}

func (t *readTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to read",
			},
		},
		"required": []string{"path"},
	}
}

func (t *readTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path is required")
	}

	// Check policy
	allowed, reason := t.policy.CheckPath(t.Name(), path)
	if !allowed {
		return nil, fmt.Errorf("policy denied: %s", reason)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), nil
}

// writeTool implements the write tool (R5.2.2).
type writeTool struct {
	policy *policy.Policy
}

func (t *writeTool) Name() string { return "write" }

func (t *writeTool) Description() string {
	return "Write content to a file at the given path. Creates parent directories if needed."
}

func (t *writeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *writeTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}

	// Check policy
	allowed, reason := t.policy.CheckPath(t.Name(), path)
	if !allowed {
		return nil, fmt.Errorf("policy denied: %s", reason)
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return "ok", nil
}

// editTool implements the edit tool (R5.2.3).
type editTool struct {
	policy *policy.Policy
}

func (t *editTool) Name() string { return "edit" }

func (t *editTool) Description() string {
	return "Find and replace text in a file. The old text must match exactly."
}

func (t *editTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to edit",
			},
			"old": map[string]interface{}{
				"type":        "string",
				"description": "Text to find (exact match)",
			},
			"new": map[string]interface{}{
				"type":        "string",
				"description": "Text to replace with",
			},
		},
		"required": []string{"path", "old", "new"},
	}
}

func (t *editTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path is required")
	}
	old, ok := args["old"].(string)
	if !ok {
		return nil, fmt.Errorf("old is required")
	}
	new, ok := args["new"].(string)
	if !ok {
		return nil, fmt.Errorf("new is required")
	}

	// Check policy
	allowed, reason := t.policy.CheckPath(t.Name(), path)
	if !allowed {
		return nil, fmt.Errorf("policy denied: %s", reason)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	oldContent := string(content)
	if !strings.Contains(oldContent, old) {
		return nil, fmt.Errorf("pattern not found in file")
	}

	newContent := strings.Replace(oldContent, old, new, 1)
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return "ok", nil
}

// globTool implements the glob tool (R5.2.4).
type globTool struct {
	policy *policy.Policy
}

func (t *globTool) Name() string { return "glob" }

func (t *globTool) Description() string {
	return "Find files matching a glob pattern."
}

func (t *globTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Glob pattern (e.g., *.go, **/*.txt)",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *globTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	pattern, ok := args["pattern"].(string)
	if !ok {
		return nil, fmt.Errorf("pattern is required")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	return matches, nil
}

// grepTool implements the grep tool (R5.2.5).
type grepTool struct {
	policy *policy.Policy
}

func (t *grepTool) Name() string { return "grep" }

func (t *grepTool) Description() string {
	return "Search for a regex pattern in a file or directory."
}

func (t *grepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File or directory to search",
			},
		},
		"required": []string{"pattern", "path"},
	}
}

// GrepMatch represents a grep match result.
type GrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func (t *grepTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	pattern, ok := args["pattern"].(string)
	if !ok {
		return nil, fmt.Errorf("pattern is required")
	}
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path is required")
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var matches []GrepMatch

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("path not found: %w", err)
	}

	if info.IsDir() {
		err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if info.IsDir() {
				return nil
			}
			fileMatches, _ := grepFile(re, p)
			matches = append(matches, fileMatches...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		matches, err = grepFile(re, path)
		if err != nil {
			return nil, err
		}
	}

	return matches, nil
}

func grepFile(re *regexp.Regexp, path string) ([]GrepMatch, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var matches []GrepMatch
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if re.MatchString(line) {
			matches = append(matches, GrepMatch{
				File:    path,
				Line:    i + 1,
				Content: line,
			})
		}
	}
	return matches, nil
}

// lsTool implements the ls tool (R5.2.6).
type lsTool struct {
	policy *policy.Policy
}

func (t *lsTool) Name() string { return "ls" }

func (t *lsTool) Description() string {
	return "List directory contents."
}

func (t *lsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Directory path to list",
			},
		},
		"required": []string{"path"},
	}
}

func (t *lsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path is required")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var result []DirEntry
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, DirEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}

	return result, nil
}

// bashTool implements the bash tool (R5.3.1).
type bashTool struct {
	policy *policy.Policy
}

func (t *bashTool) Name() string { return "bash" }

func (t *bashTool) Description() string {
	return "Execute a shell command."
}

func (t *bashTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Shell command to execute",
			},
		},
		"required": []string{"command"},
	}
}

func (t *bashTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	command, ok := args["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command is required")
	}

	// Check policy
	allowed, reason := t.policy.CheckCommand(t.Name(), command)
	if !allowed {
		return nil, fmt.Errorf("policy denied: %s", reason)
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = t.policy.Workspace

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

// --- Web Tools (R5.4) ---

// webFetchTool implements the web_fetch tool (R5.4.1).
type webFetchTool struct {
	policy     *policy.Policy
	gatewayURL string
}

func (t *webFetchTool) Name() string { return "web_fetch" }

func (t *webFetchTool) Description() string {
	return "Fetch the full content from a URL. Use after web_search to retrieve complete information from promising results. The snippets from web_search are only previews - always fetch URLs that seem relevant to get the actual content needed for thorough research."
}

func (t *webFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to fetch (typically from web_search results)",
			},
		},
		"required": []string{"url"},
	}
}

func (t *webFetchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	url, ok := args["url"].(string)
	if !ok {
		return nil, fmt.Errorf("url is required")
	}

	// Extract domain from URL for policy check
	domain := extractDomain(url)
	allowed, reason := t.policy.CheckDomain(t.Name(), domain)
	if !allowed {
		return nil, fmt.Errorf("policy denied: %s", reason)
	}

	// TODO: Route through gateway if configured
	// For now, direct fetch
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

// webSearchTool implements the web_search tool (R5.4.2).
type webSearchTool struct {
	policy *policy.Policy
}

func (t *webSearchTool) Name() string { return "web_search" }

func (t *webSearchTool) Description() string {
	return "Search the web. Returns titles, URLs, and short snippets. IMPORTANT: Snippets are brief previews only - use web_fetch on relevant URLs to get the full content needed for research. The standard flow is: web_search to discover sources, then web_fetch on 2-4 most relevant URLs."
}

func (t *webSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (1-10, default 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *webSearchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	query, ok := args["query"].(string)
	if !ok {
		return nil, fmt.Errorf("query is required")
	}

	count := 5
	if c, ok := args["count"].(float64); ok {
		count = int(c)
		if count < 1 {
			count = 1
		} else if count > 10 {
			count = 10
		}
	}

	// Try Brave first, then Tavily
	if apiKey := os.Getenv("BRAVE_API_KEY"); apiKey != "" {
		return searchBrave(ctx, query, count, apiKey)
	}
	if apiKey := os.Getenv("TAVILY_API_KEY"); apiKey != "" {
		return searchTavily(ctx, query, count, apiKey)
	}

	return nil, fmt.Errorf("no search API configured. Set BRAVE_API_KEY or TAVILY_API_KEY in credentials.toml")
}

// SearchResult represents a single search result
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// searchBrave searches using Brave Search API
func searchBrave(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		strings.ReplaceAll(query, " ", "+"), count)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search error (%d): %s", resp.StatusCode, string(body))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	results := make([]SearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}
	return results, nil
}

// searchTavily searches using Tavily API
func searchTavily(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	reqBody := map[string]interface{}{
		"api_key":     apiKey,
		"query":       query,
		"max_results": count,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily search error (%d): %s", resp.StatusCode, string(body))
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily response: %w", err)
	}

	results := make([]SearchResult, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return results, nil
}

// extractDomain extracts the domain from a URL.
func extractDomain(urlStr string) string {
	// Simple extraction - in production use net/url
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")
	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	if idx := strings.Index(urlStr, ":"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	return urlStr
}

// --- Memory Tools (R5.5) ---

// memoryReadTool implements the memory_read tool (R5.5.1).
type memoryReadTool struct {
	store MemoryStore
}

func (t *memoryReadTool) Name() string { return "memory_read" }

func (t *memoryReadTool) Description() string {
	return "Read a value from persistent memory by key."
}

func (t *memoryReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key to read",
			},
		},
		"required": []string{"key"},
	}
}

func (t *memoryReadTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	key, ok := args["key"].(string)
	if !ok {
		return nil, fmt.Errorf("key is required")
	}

	value, err := t.store.Get(key)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// memoryWriteTool implements the memory_write tool (R5.5.2).
type memoryWriteTool struct {
	store MemoryStore
}

func (t *memoryWriteTool) Name() string { return "memory_write" }

func (t *memoryWriteTool) Description() string {
	return "Write a value to persistent memory."
}

func (t *memoryWriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key to write",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "Value to store",
			},
		},
		"required": []string{"key", "value"},
	}
}

func (t *memoryWriteTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	key, ok := args["key"].(string)
	if !ok {
		return nil, fmt.Errorf("key is required")
	}
	value, ok := args["value"].(string)
	if !ok {
		return nil, fmt.Errorf("value is required")
	}

	if err := t.store.Set(key, value); err != nil {
		return nil, err
	}
	return "ok", nil
}

// MemoryStore is the interface for persistent key-value storage.
type MemoryStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// FileMemoryStore stores memory in a JSON file.
type FileMemoryStore struct {
	path string
	data map[string]string
}

// NewFileMemoryStore creates a new file-based memory store.
func NewFileMemoryStore(path string) *FileMemoryStore {
	store := &FileMemoryStore{
		path: path,
		data: make(map[string]string),
	}
	// Load existing data
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &store.data)
	}
	return store
}

func (s *FileMemoryStore) Get(key string) (string, error) {
	if val, ok := s.data[key]; ok {
		return val, nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func (s *FileMemoryStore) Set(key, value string) error {
	s.data[key] = value
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
