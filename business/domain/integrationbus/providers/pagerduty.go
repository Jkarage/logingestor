package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

const pagerdutyEventsV2URL = "https://events.pagerduty.com/v2/enqueue"

// PagerDuty creates incidents via the PagerDuty Events API v2.
// The "apiKey" credential is the integration routing key (Events API key).
// The "serviceId" is stored as a custom detail for traceability.
type PagerDuty struct{}

// NewPagerDuty returns a new PagerDuty caller.
func NewPagerDuty() *PagerDuty { return &PagerDuty{} }

// Send triggers a PagerDuty incident via the Events API v2.
func (p *PagerDuty) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	routingKey := creds["apiKey"]
	if routingKey == "" {
		return fmt.Errorf("pagerduty: missing apiKey credential")
	}

	severity := "info"
	switch payload.Level {
	case "ERROR":
		severity = "critical"
	case "WARN":
		severity = "warning"
	}

	body := map[string]any{
		"routing_key":  routingKey,
		"event_action": "trigger",
		"dedup_key":    payload.LogID,
		"payload": map[string]any{
			"summary":   fmt.Sprintf("[%s] %s: %s", payload.Level, payload.ProjectName, payload.Message),
			"source":    payload.Source,
			"severity":  severity,
			"timestamp": payload.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			"custom_details": map[string]string{
				"project":    payload.ProjectName,
				"log_id":     payload.LogID,
				"service_id": creds["serviceId"],
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("pagerduty: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pagerdutyEventsV2URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("pagerduty: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty: do: %w", err)
	}
	defer resp.Body.Close()

	// PagerDuty returns 202 Accepted on success.
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("pagerduty: unexpected status %d", resp.StatusCode)
	}

	return nil
}
