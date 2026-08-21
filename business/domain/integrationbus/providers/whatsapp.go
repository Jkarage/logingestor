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

// WhatsApp sends alerts through the WhatsApp Business Cloud API.
//
// There is a constraint here that no other provider has: WhatsApp only allows a
// free-form text message within 24 hours of the recipient last messaging the
// business. Outside that window the message must be a pre-approved template.
// An alerting integration is almost always outside it, so a template name is
// supported and is the configuration we recommend; without one this sends text
// and Meta will reject it once the window has closed.
type WhatsApp struct{}

// NewWhatsApp returns a new WhatsApp caller.
func NewWhatsApp() *WhatsApp { return &WhatsApp{} }

// Send posts a message to one recipient via the Cloud API.
func (p *WhatsApp) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	phoneNumberID := creds["phoneNumberId"]
	accessToken := creds["accessToken"]
	to := creds["to"]

	switch {
	case phoneNumberID == "":
		return fmt.Errorf("whatsapp: missing phoneNumberId credential")
	case accessToken == "":
		return fmt.Errorf("whatsapp: missing accessToken credential")
	case to == "":
		return fmt.Errorf("whatsapp: missing to credential")
	}

	text := alertText(
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
	)

	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
	}

	if template := creds["templateName"]; template != "" {
		language := creds["templateLanguage"]
		if language == "" {
			language = "en"
		}

		// One body parameter carrying the whole alert line. A template with a
		// different number of placeholders will be rejected by Meta, which is the
		// correct failure — the template is the customer's to define.
		body["type"] = "template"
		body["template"] = map[string]any{
			"name":     template,
			"language": map[string]string{"code": language},
			"components": []map[string]any{
				{
					"type": "body",
					"parameters": []map[string]string{
						{"type": "text", "text": text},
					},
				},
			},
		}
	} else {
		body["type"] = "text"
		body["text"] = map[string]any{"body": text, "preview_url": false}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", whatsappBase, phoneNumberID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("whatsapp: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Meta's errors are specific and worth surfacing verbatim: "template not
		// found" and "outside the 24 hour window" are different problems with
		// different fixes.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("whatsapp: unexpected status %d: %s", resp.StatusCode, string(b))
	}

	return nil
}
