package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// BeemSMS sends SMS alerts via the Beem Africa Messaging API.
// Auth: HTTP Basic Auth — API Key as username, Secret Key as password.
type BeemSMS struct{}

// NewBeemSMS returns a new BeemSMS caller.
func NewBeemSMS() *BeemSMS { return &BeemSMS{} }

// beemResponse is the top-level shape of a Beem Africa API response.
type beemResponse struct {
	Code int    `json:"code"`
	Message string `json:"message"`
}

// Send posts an SMS to the configured recipient via the Beem Africa API.
func (p *BeemSMS) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	apiKey := creds["apiKey"]
	secretKey := creds["secretKey"]
	senderID := creds["senderId"]
	to := creds["to"]

	switch {
	case apiKey == "":
		return fmt.Errorf("beemsms: missing apiKey credential")
	case secretKey == "":
		return fmt.Errorf("beemsms: missing secretKey credential")
	case senderID == "":
		return fmt.Errorf("beemsms: missing senderId credential")
	case to == "":
		return fmt.Errorf("beemsms: missing to credential")
	}

	message := fmt.Sprintf("[%s] %s: %s (source: %s, %s)",
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
	)

	// Beem Africa expects JSON with source_addr, message, and a recipients array.
	reqBody := map[string]any{
		"source_addr": senderID,
		"encoding":    0, // 0 = GSM7 (standard Latin charset, 160 chars/SMS)
		"message":     message,
		"recipients": []map[string]any{
			{"recipient_id": 1, "dest_addr": to},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("beemsms: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://apisms.beem.africa/v1/send", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("beemsms: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(apiKey, secretKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("beemsms: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("beemsms: unexpected status %d: %s", resp.StatusCode, string(b))
	}

	// Beem returns HTTP 200 even on logical errors; code 100 means success.
	var result beemResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("beemsms: decode response: %w", err)
	}
	if result.Code != 100 {
		return fmt.Errorf("beemsms: api error %d: %s", result.Code, result.Message)
	}

	return nil
}
