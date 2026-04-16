package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Jira opens issues on a Jira Cloud project when alerts fire.
// Authentication uses Basic auth with email:token (Jira Cloud API token).
type Jira struct{}

// NewJira returns a new Jira caller.
func NewJira() *Jira { return &Jira{} }

// Send creates a Jira issue via the REST API v3.
func (p *Jira) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	domain := creds["domain"]
	if domain == "" {
		return fmt.Errorf("jira: missing domain credential")
	}

	email := creds["email"]
	if email == "" {
		return fmt.Errorf("jira: missing email credential")
	}

	token := creds["token"]
	if token == "" {
		return fmt.Errorf("jira: missing token credential")
	}

	project := creds["project"]
	if project == "" {
		return fmt.Errorf("jira: missing project credential")
	}

	body := map[string]any{
		"fields": map[string]any{
			"project":     map[string]string{"key": project},
			"summary":     fmt.Sprintf("[%s] %s: %s", payload.Level, payload.ProjectName, payload.Message),
			"description": map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []map[string]any{
					{
						"type": "paragraph",
						"content": []map[string]any{
							{
								"type": "text",
								"text": fmt.Sprintf(
									"Source: %s\nLog ID: %s\nTime: %s",
									payload.Source,
									payload.LogID,
									payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
								),
							},
						},
					},
				},
			},
			"issuetype": map[string]string{"name": "Bug"},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("jira: marshal: %w", err)
	}

	url := fmt.Sprintf("https://%s/rest/api/3/issue", domain)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("jira: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("jira: do: %w", err)
	}
	defer resp.Body.Close()

	// Jira returns 201 Created on success.
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("jira: unexpected status %d", resp.StatusCode)
	}

	return nil
}
