package providers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Webhook POSTs a structured JSON alert to any HTTP endpoint.
// If a "secret" credential is provided, a SHA-256 HMAC signature is added
// as the X-LoginGestor-Signature header.
type Webhook struct{}

// NewWebhook returns a new Webhook caller.
func NewWebhook() *Webhook { return &Webhook{} }

// Send POSTs the alert payload as JSON to the target URL.
func (p *Webhook) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	targetURL := creds["url"]
	if targetURL == "" {
		return fmt.Errorf("webhook: missing url credential")
	}

	body := map[string]any{
		"project": payload.ProjectName,
		"level":   payload.Level,
		"message": payload.Message,
		"source":  payload.Source,
		"log_id":  payload.LogID,
		"ts":      payload.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if secret := creds["secret"]; secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(data)
		req.Header.Set("X-LoginGestor-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: unexpected status %d", resp.StatusCode)
	}

	return nil
}
