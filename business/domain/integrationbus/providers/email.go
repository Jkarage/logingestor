package providers

import (
	"context"
	"fmt"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
)

// Email sends alert notifications via the Resend email service.
// The "to" credential contains the recipient address.
// Sending is done through the system-level Resend configuration — no SMTP
// credentials are used from the user's input in v1.
type Email struct {
	mailer *emailer.Config
}

// NewEmail returns a new Email caller backed by the given Resend mailer.
func NewEmail(mailer *emailer.Config) *Email {
	return &Email{mailer: mailer}
}

// Send dispatches an alert email to the address stored in the "to" credential.
func (p *Email) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	to := creds["to"]
	if to == "" {
		return fmt.Errorf("email: missing 'to' credential")
	}

	subject := fmt.Sprintf("[%s] %s – LoginGestor Alert", payload.Level, payload.ProjectName)
	body := fmt.Sprintf(
		"Project: %s\nLevel:   %s\nMessage: %s\nSource:  %s\nLog ID:  %s\nTime:    %s",
		payload.ProjectName,
		payload.Level,
		payload.Message,
		payload.Source,
		payload.LogID,
		payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
	)

	if err := p.mailer.SendAlert(to, subject, body); err != nil {
		return fmt.Errorf("email: send: %w", err)
	}

	return nil
}
