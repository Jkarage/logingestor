// Package analyzebus provides AI-powered log analysis via Cerebrium inference.
package analyzebus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	log        *logger.Logger
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewBusiness constructs an analyze business API for use.
func NewBusiness(log *logger.Logger, baseURL, apiKey string) *Business {
	return &Business{
		log:        log,
		httpClient: &http.Client{Timeout: 120 * time.Second},
		baseURL:    baseURL,
		apiKey:     apiKey,
	}
}

// =============================================================================
// Cerebrium OpenAI-compatible request/response shapes

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

// cerebriumResponse is the outer envelope Cerebrium wraps around the model output.
type cerebriumResponse struct {
	Result struct {
		Choices []chatChoice `json:"choices"`
	} `json:"result"`
}

// =============================================================================

const analyzePrompt = `You are an expert software engineer and SRE specializing in log analysis.
Analyze the following log entry and respond with ONLY a JSON object containing exactly these fields:
- "summary": one-sentence plain-English description of what happened
- "likely_cause": the most probable root cause of this log event
- "suggested_fixes": array of 2-4 concrete, actionable steps to resolve or investigate this issue
- "related_patterns": array of 2-3 related log patterns or error signatures that often accompany this issue

Respond with only the JSON object and nothing else.

Log entry:
`

// Analyze calls the Cerebrium inference endpoint and returns a structured analysis.
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

	logData, err := json.Marshal(logPayload{
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

	reqBody, err := json.Marshal(chatRequest{
		Messages: []chatMessage{
			{Role: "user", Content: analyzePrompt + string(logData)},
		},
		MaxTokens:   1024,
		Temperature: 0.1,
	})
	if err != nil {
		return Analysis{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return Analysis{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return Analysis{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Analysis{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Analysis{}, fmt.Errorf("cerebrium returned %d: %s", resp.StatusCode, body)
	}

	var cr cerebriumResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return Analysis{}, fmt.Errorf("decode response: %w", err)
	}

	if len(cr.Result.Choices) == 0 {
		return Analysis{}, fmt.Errorf("no choices in response")
	}

	text := cr.Result.Choices[0].Message.Content
	if text == "" {
		return Analysis{}, fmt.Errorf("empty content in response")
	}

	text = extractJSON(text)

	var result Analysis
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		b.log.Error(ctx, "analyzebus: parse model output", "raw", text, "err", err)
		return Analysis{}, fmt.Errorf("parse model output: %w", err)
	}

	return result, nil
}

// extractJSON strips markdown code fences and surrounding whitespace so the
// raw model output can be passed directly to json.Unmarshal.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	// Strip opening fence: ```json or ```
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
	}

	// Strip closing fence
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}

	return strings.TrimSpace(s)
}
