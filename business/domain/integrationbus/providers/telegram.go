package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Telegram sends alerts as bot messages via the Telegram Bot API.
type Telegram struct{}

// NewTelegram returns a new Telegram caller.
func NewTelegram() *Telegram { return &Telegram{} }

// Send posts a formatted message to the configured Telegram chat.
func (p *Telegram) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	botToken := creds["botToken"]
	if botToken == "" {
		return fmt.Errorf("telegram: missing botToken credential")
	}

	chatID := creds["chatId"]
	if chatID == "" {
		return fmt.Errorf("telegram: missing chatId credential")
	}

	text := fmt.Sprintf(
		"*[%s] %s*\n%s\n\n_Source_: %s\n_Time_: %s",
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
	)

	body := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("telegram: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: unexpected status %d", resp.StatusCode)
	}

	return nil
}
