package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// AfricasTalking sends SMS alerts through Africa's Talking.
//
// Two details differ from every other provider here: the request is form
// encoded rather than JSON, and the sandbox is a separate host that only
// accepts the username "sandbox". Getting either wrong fails with an
// unhelpful 401, so both are explicit.
type AfricasTalking struct{}

// NewAfricasTalking returns a new Africa's Talking caller.
func NewAfricasTalking() *AfricasTalking { return &AfricasTalking{} }

// atResponse is the slice of the response this caller reads. A request can be
// accepted overall while an individual recipient is refused, so the per
// recipient status is what decides success.
type atResponse struct {
	SMSMessageData struct {
		Message    string `json:"Message"`
		Recipients []struct {
			Number     string `json:"number"`
			Status     string `json:"status"`
			StatusCode int    `json:"statusCode"`
		} `json:"Recipients"`
	} `json:"SMSMessageData"`
}

// Send posts an SMS to the configured recipient.
func (p *AfricasTalking) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	username := creds["username"]
	apiKey := creds["apiKey"]
	to := creds["to"]

	switch {
	case username == "":
		return fmt.Errorf("africastalking: missing username credential")
	case apiKey == "":
		return fmt.Errorf("africastalking: missing apiKey credential")
	case to == "":
		return fmt.Errorf("africastalking: missing to credential")
	}

	base := atLiveBase
	if strings.EqualFold(creds["sandbox"], "true") || strings.EqualFold(username, "sandbox") {
		base = atSandboxBase
	}

	form := url.Values{}
	form.Set("username", username)
	form.Set("to", to)
	form.Set("message", alertText(
		payload.Level,
		payload.ProjectName,
		payload.Message,
		payload.Source,
		payload.Timestamp.UTC().Format("2006-01-02 15:04 UTC"),
	))

	// An alphanumeric sender id has to be registered with Africa's Talking; left
	// empty, messages go out from the shared short code.
	if from := creds["from"]; from != "" {
		form.Set("from", from)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/version1/messaging", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("africastalking: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("apiKey", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("africastalking: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("africastalking: unexpected status %d", resp.StatusCode)
	}

	var result atResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("africastalking: decode response: %w", err)
	}

	if len(result.SMSMessageData.Recipients) == 0 {
		// No recipient row at all means nothing was queued; the summary message
		// carries the reason, commonly an invalid number or no credit.
		return fmt.Errorf("africastalking: no recipients accepted: %s", result.SMSMessageData.Message)
	}

	// 100 processed, 101 sent, 102 queued. Anything else is a refusal.
	for _, r := range result.SMSMessageData.Recipients {
		if r.StatusCode < 100 || r.StatusCode > 102 {
			return fmt.Errorf("africastalking: %s refused (%d): %s", r.Number, r.StatusCode, r.Status)
		}
	}

	return nil
}
