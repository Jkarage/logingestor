package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Discord sends alerts to a Discord channel via an incoming webhook.
type Discord struct{}

// NewDiscord returns a new Discord caller.
func NewDiscord() *Discord { return &Discord{} }

// Send posts an embed message to the configured Discord webhook URL.
func (p *Discord) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	webhookURL := creds["webhookUrl"]
	if webhookURL == "" {
		return fmt.Errorf("discord: missing webhookUrl credential")
	}

	color := 0x36a64f // green default
	switch payload.Level {
	case "ERROR":
		color = 0xe74c3c
	case "WARN":
		color = 0xf39c12
	case "DEBUG":
		color = 0x95a5a6
	}

	body := map[string]any{
		"embeds": []map[string]any{
			{
				"title":       fmt.Sprintf("[%s] %s", payload.Level, payload.ProjectName),
				"description": payload.Message,
				"color":       color,
				"footer": map[string]string{
					"text": fmt.Sprintf("%s • %s", payload.Source, payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC")),
				},
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("discord: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("discord: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord: do: %w", err)
	}
	defer resp.Body.Close()

	// Discord returns 204 No Content on success.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("discord: unexpected status %d", resp.StatusCode)
	}

	return nil
}
