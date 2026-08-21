package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Linear creates an issue when an alert fires.
//
// Linear is GraphQL-only, and it answers 200 even when the mutation failed —
// the failure is in the response body. Checking the status alone would report
// every rejected issue as delivered, so the body is decoded.
type Linear struct{}

// NewLinear returns a new Linear caller.
func NewLinear() *Linear { return &Linear{} }

// issueCreateMutation is kept minimal on purpose: asking for more fields back
// means more that can change under us.
const issueCreateMutation = `mutation IssueCreate($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { identifier } }
}`

// linearResponse is the slice of the GraphQL envelope this caller reads.
type linearResponse struct {
	Data struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Send creates an issue on the configured team.
func (p *Linear) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	apiKey := creds["apiKey"]
	teamID := creds["teamId"]

	switch {
	case apiKey == "":
		return fmt.Errorf("linear: missing apiKey credential")
	case teamID == "":
		return fmt.Errorf("linear: missing teamId credential")
	}

	input := map[string]any{
		"teamId": teamID,
		"title":  fmt.Sprintf("[%s] %s: %s", payload.Level, payload.ProjectName, payload.Message),
		"description": fmt.Sprintf(
			"**Level:** %s\n**Source:** %s\n**Time:** %s\n**Log ID:** %s\n\n%s",
			payload.Level,
			payload.Source,
			payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
			payload.LogID,
			payload.Message,
		),
	}

	if priority := creds["priority"]; priority != "" {
		// Linear priorities are 0 (none) to 4 (low). An out-of-range value is
		// rejected by the API, which is the right place for that check.
		var n int
		if _, err := fmt.Sscanf(priority, "%d", &n); err == nil {
			input["priority"] = n
		}
	}

	data, err := json.Marshal(map[string]any{
		"query":     issueCreateMutation,
		"variables": map[string]any{"input": input},
	})
	if err != nil {
		return fmt.Errorf("linear: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, linearEndpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("linear: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// A personal API key is sent raw; only OAuth access tokens use the Bearer
	// prefix. Sending "Bearer <personal key>" is rejected as unauthenticated.
	req.Header.Set("Authorization", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: unexpected status %d", resp.StatusCode)
	}

	var result linearResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("linear: decode response: %w", err)
	}

	if len(result.Errors) > 0 {
		return fmt.Errorf("linear: api error: %s", result.Errors[0].Message)
	}
	if !result.Data.IssueCreate.Success {
		return fmt.Errorf("linear: issue was not created")
	}

	return nil
}
