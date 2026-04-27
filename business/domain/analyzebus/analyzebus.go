// Package analyzebus provides AI-powered log analysis using Claude.
package analyzebus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Analysis is the structured result of an AI-powered log analysis.
type Analysis struct {
	Summary         string   `json:"summary"`
	LikelyCause     string   `json:"likely_cause"`
	SuggestedFixes  []string `json:"suggested_fixes"`
	RelatedPatterns []string `json:"related_patterns"`
}

// Business manages the set of APIs for the analyze domain.
type Business struct {
	log    *logger.Logger
	client anthropic.Client
}

// NewBusiness constructs an analyze business API for use.
func NewBusiness(log *logger.Logger, apiKey string) *Business {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Business{log: log, client: client}
}

const analyzePrompt = `You are an expert software engineer and SRE specializing in log analysis.
Analyze the following log entry and respond with ONLY a JSON object containing exactly these fields:
- "summary": one-sentence plain-English description of what happened
- "likely_cause": the most probable root cause of this log event
- "suggested_fixes": array of 2-4 concrete, actionable steps to resolve or investigate this issue
- "related_patterns": array of 2-3 related log patterns or error signatures that often accompany this issue

Respond with only the JSON object and nothing else.

Log entry:
`

// Analyze uses Claude to produce a structured analysis of a log entry.
func (b *Business) Analyze(ctx context.Context, l logbus.Log) (Analysis, error) {
	type logPayload struct {
		ID        string         `json:"id"`
		Level     string         `json:"level"`
		Message   string         `json:"message"`
		Source    string         `json:"source"`
		Timestamp string         `json:"timestamp"`
		Tags      []string       `json:"tags"`
		Meta      map[string]any `json:"meta"`
	}

	data, err := json.Marshal(logPayload{
		ID:        l.ID.String(),
		Level:     l.Level.String(),
		Message:   l.Message,
		Source:    l.Source,
		Timestamp: l.Timestamp.UTC().Format(time.RFC3339),
		Tags:      l.Tags,
		Meta:      l.Meta,
	})
	if err != nil {
		return Analysis{}, fmt.Errorf("marshal log: %w", err)
	}

	stream := b.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model("claude-opus-4-7"),
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(analyzePrompt + string(data))),
		},
	})

	msg := anthropic.Message{}
	for stream.Next() {
		msg.Accumulate(stream.Current())
	}
	if err := stream.Err(); err != nil {
		return Analysis{}, fmt.Errorf("stream: %w", err)
	}

	var text string
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text = t.Text
			break
		}
	}

	if text == "" {
		return Analysis{}, fmt.Errorf("empty response from model")
	}

	var result Analysis
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return Analysis{}, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
}
