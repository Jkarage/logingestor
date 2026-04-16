package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

const opsgenieAlertsURL = "https://api.opsgenie.com/v2/alerts"

// OpsGenie creates OpsGenie alerts for on-call escalation.
type OpsGenie struct{}

// NewOpsGenie returns a new OpsGenie caller.
func NewOpsGenie() *OpsGenie { return &OpsGenie{} }

// Send creates an OpsGenie alert using the REST API.
func (p *OpsGenie) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	apiKey := creds["apiKey"]
	if apiKey == "" {
		return fmt.Errorf("opsgenie: missing apiKey credential")
	}

	priority := "P3"
	switch payload.Level {
	case "ERROR":
		priority = "P1"
	case "WARN":
		priority = "P2"
	}

	body := map[string]any{
		"message":  fmt.Sprintf("[%s] %s: %s", payload.Level, payload.ProjectName, payload.Message),
		"alias":    payload.LogID,
		"source":   payload.Source,
		"priority": priority,
		"details": map[string]string{
			"project": payload.ProjectName,
			"level":   payload.Level,
			"log_id":  payload.LogID,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("opsgenie: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opsgenieAlertsURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("opsgenie: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "GenieKey "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("opsgenie: do: %w", err)
	}
	defer resp.Body.Close()

	// OpsGenie returns 202 Accepted on success.
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("opsgenie: unexpected status %d", resp.StatusCode)
	}

	return nil
}
