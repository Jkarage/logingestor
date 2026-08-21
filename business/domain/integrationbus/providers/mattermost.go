package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Mattermost posts alerts to a Mattermost incoming webhook.
//
// Mattermost is usually self-hosted, so the whole URL is the customer's —
// including the host. Its incoming webhooks accept Slack's payload shape, which
// is why this is a near-copy of the Slack caller rather than something new.
type Mattermost struct{}

// NewMattermost returns a new Mattermost caller.
func NewMattermost() *Mattermost { return &Mattermost{} }

// Send posts a message to the configured webhook URL.
func (p *Mattermost) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	webhookURL := creds["webhookUrl"]
	if webhookURL == "" {
		return fmt.Errorf("mattermost: missing webhookUrl credential")
	}

	body := map[string]any{
		"text": fmt.Sprintf("**[%s] %s** — %s\n_%s_ at %s",
			payload.Level,
			payload.ProjectName,
			payload.Message,
			payload.Source,
			payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
		),
	}

	// An incoming webhook is bound to a default channel; overriding it is
	// optional and only allowed if the webhook was created that way.
	if channel := creds["channel"]; channel != "" {
		body["channel"] = channel
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("mattermost: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("mattermost: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mattermost: unexpected status %d", resp.StatusCode)
	}

	return nil
}
