// Package memory provides semantic memory storage with vector embeddings.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vinayprograms/agent/internal/llm"
)

// Observation represents extracted observations from a step.
type Observation struct {
	// Findings are factual discoveries (e.g., "API rate limit is 100/min")
	Findings []string `json:"findings,omitempty"`

	// Insights are conclusions or inferences (e.g., "REST is better than GraphQL for this use case")
	Insights []string `json:"insights,omitempty"`

	// Lessons are learnings for future (e.g., "Avoid library X - it lacks TypeScript support")
	Lessons []string `json:"lessons,omitempty"`

	// StepName identifies the source step
	StepName string `json:"step_name,omitempty"`

	// StepType is the type of step (GOAL, AGENT, RUN)
	StepType string `json:"step_type,omitempty"`
}

// ObservationExtractor extracts observations from step outputs using an LLM.
type ObservationExtractor struct {
	provider llm.Provider
}

// NewObservationExtractor creates a new observation extractor.
func NewObservationExtractor(provider llm.Provider) *ObservationExtractor {
	return &ObservationExtractor{
		provider: provider,
	}
}

// extractionPrompt is the system prompt for extraction.
const extractionPrompt = `You are an observation extractor. Given the output of an agent step, extract:

1. **Findings**: Factual discoveries (facts, data, configurations found)
2. **Insights**: Conclusions, inferences, or decisions made
3. **Lessons**: Learnings that should guide future work (what to do/avoid)

Return a JSON object with these arrays. Be concise - each item should be a single sentence.
Only include meaningful observations. If a category has nothing, return an empty array.

Example output:
{
  "findings": ["The API rate limit is 100 requests per minute", "Authentication uses OAuth2"],
  "insights": ["REST is more suitable than GraphQL for this simple use case"],
  "lessons": ["Avoid using library X - it lacks TypeScript support"]
}`

// Extract extracts observations from step output.
// Returns interface{} to match executor.ObservationExtractor interface.
func (e *ObservationExtractor) Extract(ctx context.Context, stepName, stepType, output string) (interface{}, error) {
	if e.provider == nil {
		return nil, nil
	}

	// Skip if output is too short or empty
	if len(strings.TrimSpace(output)) < 50 {
		return nil, nil
	}

	// Truncate very long outputs
	if len(output) > 4000 {
		output = output[:4000] + "\n...[truncated]"
	}

	userMessage := fmt.Sprintf("Step: %s (%s)\n\nOutput:\n%s\n\nReturn ONLY a JSON object with no markdown formatting.", stepName, stepType, output)

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: extractionPrompt},
			{Role: "user", Content: userMessage},
		},
	})
	if err != nil {
		// Don't fail the step if extraction fails
		return nil, nil
	}

	// Parse JSON response
	var obs Observation
	content := strings.TrimSpace(resp.Content)

	// Try to extract JSON from the response (may be wrapped in markdown)
	jsonStr := content
	if strings.HasPrefix(content, "```") {
		// Remove markdown code block
		lines := strings.Split(content, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock || !strings.HasPrefix(content, "```") {
				jsonLines = append(jsonLines, line)
			}
		}
		jsonStr = strings.Join(jsonLines, "\n")
	}

	// Find JSON object bounds
	jsonStart := strings.Index(jsonStr, "{")
	jsonEnd := strings.LastIndex(jsonStr, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr = jsonStr[jsonStart : jsonEnd+1]
	}

	if err := json.Unmarshal([]byte(jsonStr), &obs); err != nil {
		return nil, nil
	}

	obs.StepName = stepName
	obs.StepType = stepType

	return &obs, nil
}

// BleveObservationStore implements observation storage using BleveStore.
type BleveObservationStore struct {
	store *BleveStore
}

// NewBleveObservationStore creates an observation store backed by BleveStore.
func NewBleveObservationStore(store *BleveStore) *BleveObservationStore {
	return &BleveObservationStore{store: store}
}

// StoreObservation stores an observation in the Bleve store.
// Each finding/insight/lesson is stored as a separate document with its category.
func (s *BleveObservationStore) StoreObservation(ctx context.Context, obsRaw interface{}) error {
	obs, ok := obsRaw.(*Observation)
	if obs == nil || !ok {
		return nil
	}

	source := fmt.Sprintf("%s:%s", obs.StepType, obs.StepName)

	// Store findings
	for _, finding := range obs.Findings {
		s.store.RememberObservation(ctx, finding, "finding", source)
	}

	// Store insights
	for _, insight := range obs.Insights {
		s.store.RememberObservation(ctx, insight, "insight", source)
	}

	// Store lessons
	for _, lesson := range obs.Lessons {
		s.store.RememberObservation(ctx, lesson, "lesson", source)
	}

	return nil
}

// QueryRelevantObservations retrieves observations relevant to a query as FIL.
// Returns []interface{} to match executor.ObservationStore interface.
func (s *BleveObservationStore) QueryRelevantObservations(ctx context.Context, query string, limit int) ([]interface{}, error) {
	fil, err := s.store.RecallFIL(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	if fil == nil || (len(fil.Findings)+len(fil.Insights)+len(fil.Lessons) == 0) {
		return nil, nil
	}

	// Convert to Observation for interface compatibility
	obs := &Observation{
		Findings: fil.Findings,
		Insights: fil.Insights,
		Lessons:  fil.Lessons,
	}

	return []interface{}{obs}, nil
}
