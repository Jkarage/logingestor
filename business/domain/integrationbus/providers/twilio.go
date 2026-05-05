package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// Twilio sends SMS alerts via the Twilio Messaging API.
// Auth: HTTP Basic Auth — Account SID as username, Auth Token as password.
type Twilio struct{}

// NewTwilio returns a new Twilio caller.
func NewTwilio() *Twilio { return &Twilio{} }

// Send posts an SMS to the configured recipient via the Twilio Messages API.
func (p *Twilio) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	accountSid := creds["accountSid"]
	authToken := creds["authToken"]
	from := creds["from"]
	to := creds["to"]

	switch {
	case accountSid == "":
		return fmt.Errorf("twilio: missing accountSid credential")
	case authToken == "":
		return fmt.Errorf("twilio: missing authToken credential")
	case from == "":
		return fmt.Errorf("twilio: missing from credential")
	case to == "":
		return fmt.Errorf("twilio: missing to credential")
	}

	body := fmt.Sprintf("[%s] %s: %s (source: %s, %s)",
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
	)

	// Twilio Messages API requires application/x-www-form-urlencoded, not JSON.
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", from)
	form.Set("Body", body)

	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", accountSid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("twilio: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(accountSid, authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("twilio: do: %w", err)
	}
	defer resp.Body.Close()

	// Twilio returns 201 Created on success.
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio: unexpected status %d: %s", resp.StatusCode, string(b))
	}

	return nil
}
