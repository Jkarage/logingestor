package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// MSTeams posts alerts into a Microsoft Teams channel.
//
// The URL comes from Teams' Workflows connector ("Post to a channel when a
// webhook request is received"), which supersedes the retired Office 365
// connectors. Workflows expects an Adaptive Card wrapped in a message
// attachment; the old connectors expected a MessageCard, so a card sent to a
// surviving legacy URL will not render — that is a reason to re-create the
// connector, not to send both shapes.
type MSTeams struct{}

// NewMSTeams returns a new Microsoft Teams caller.
func NewMSTeams() *MSTeams { return &MSTeams{} }

// Send posts an Adaptive Card to the configured workflow URL.
func (p *MSTeams) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	webhookURL := creds["webhookUrl"]
	if webhookURL == "" {
		return fmt.Errorf("msteams: missing webhookUrl credential")
	}

	when := payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC")

	card := map[string]any{
		"type":    "AdaptiveCard",
		"version": "1.4",
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"body": []map[string]any{
			{
				"type":   "TextBlock",
				"text":   fmt.Sprintf("%s · %s", payload.Level, payload.ProjectName),
				"weight": "Bolder",
				"size":   "Medium",
				"wrap":   true,
				// Teams renders "attention" as red, which is what an ERROR should
				// look like at a glance in a busy channel.
				"color": teamsColour(payload.Level),
			},
			{
				"type": "TextBlock",
				"text": payload.Message,
				"wrap": true,
			},
			{
				"type": "FactSet",
				"facts": []map[string]string{
					{"title": "Source", "value": payload.Source},
					{"title": "Time", "value": when},
				},
			},
		},
	}

	body := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"contentUrl":  nil,
				"content":     card,
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("msteams: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("msteams: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("msteams: do: %w", err)
	}
	defer resp.Body.Close()

	// A workflow accepts the request and runs asynchronously, so it answers 202;
	// a legacy connector answered 200. Both are success, and neither says whether
	// the card rendered.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("msteams: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// teamsColour maps a level onto the Adaptive Card colour names.
func teamsColour(level string) string {
	switch level {
	case "ERROR":
		return "attention"
	case "WARN":
		return "warning"
	default:
		return "default"
	}
}
