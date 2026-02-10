// Package llm provides LLM provider interfaces and implementations.
package llm

import (
	"context"
	"fmt"
)

// Summarizer uses an LLM to summarize content and answer questions
type Summarizer struct {
	provider Provider
}

// NewSummarizer creates a new summarizer with the given LLM provider
func NewSummarizer(provider Provider) *Summarizer {
	return &Summarizer{provider: provider}
}

// Summarize extracts information from content based on a question
func (s *Summarizer) Summarize(ctx context.Context, content, question string) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("no LLM provider configured for summarization")
	}

	// Build the prompt (similar to Claude Code's approach)
	prompt := fmt.Sprintf(`Web page content:
---
%s
---

%s

Provide a concise response based only on the content above. In your response:
- Keep the answer focused and relevant to the question
- Use quotation marks for exact language from the content
- Limit quotes to 125 characters maximum
- If the content doesn't contain relevant information, say so
- Be concise but thorough`, content, question)

	resp, err := s.provider.Chat(ctx, ChatRequest{
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 1000, // Keep responses concise
	})
	if err != nil {
		return "", fmt.Errorf("summarization LLM call failed: %w", err)
	}

	return resp.Content, nil
}
