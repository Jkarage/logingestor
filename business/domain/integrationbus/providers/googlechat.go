package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// GoogleChat posts alerts into a Google Chat space.
//
// The URL is a space's incoming webhook, which already carries its own key and
// token query parameters — there is nothing else to authenticate with.
type GoogleChat struct{}

// NewGoogleChat returns a new Google Chat caller.
func NewGoogleChat() *GoogleChat { return &GoogleChat{} }

// Send posts a message to the configured space webhook.
func (p *GoogleChat) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	webhookURL := creds["webhookUrl"]
	if webhookURL == "" {
		return fmt.Errorf("googlechat: missing webhookUrl credential")
	}

	// Google Chat renders a small subset of markdown in plain text messages:
	// *bold* and _italic_ work, headings and links do not.
	text := fmt.Sprintf("*[%s] %s*\n%s\n_%s_ at %s",
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
	)

	data, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("googlechat: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("googlechat: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("googlechat: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("googlechat: unexpected status %d", resp.StatusCode)
	}

	return nil
}
