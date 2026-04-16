// Package providers contains per-provider implementations of integrationbus.Caller.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Slack sends alerts to a Slack channel via an incoming webhook.
type Slack struct{}

// NewSlack returns a new Slack caller.
func NewSlack() *Slack { return &Slack{} }

// Send posts a formatted message to the configured Slack webhook URL.
func (p *Slack) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	webhookURL := creds["webhookUrl"]
	if webhookURL == "" {
		return fmt.Errorf("slack: missing webhookUrl credential")
	}

	body := map[string]any{
		"text": fmt.Sprintf("[%s] *%s* — %s\n_%s_ at %s",
			payload.Level,
			payload.ProjectName,
			payload.Message,
			payload.Source,
			payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
		),
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("slack: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("slack: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack: unexpected status %d", resp.StatusCode)
	}

	return nil
}
