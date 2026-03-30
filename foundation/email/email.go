package emailer

import (
	"fmt"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

// Config holds SendGrid configuration.
type Config struct {
	APIKey    string
	FromName  string
	FromEmail string
}

func New(apiKey, from, fromName string) *Config {
	return &Config{
		APIKey:    apiKey,
		FromName:  fromName,
		FromEmail: from,
	}
}

// SendVerification sends an HTML verification email to the given address.
func (e *Config) SendVerification(toEmail, toName, verifyURL string) error {
	from := mail.NewEmail(e.FromName, e.FromEmail)
	to := mail.NewEmail(toName, toEmail)

	m := mail.NewSingleEmail(from, "Verify your email address", to, plainBody(verifyURL), htmlBody(verifyURL))

	client := sendgrid.NewSendClient(e.APIKey)
	resp, err := client.Send(m)
	if err != nil {
		return fmt.Errorf("sendgrid send: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sendgrid error status[%d] body[%s]", resp.StatusCode, resp.Body)
	}

	return nil
}

// plainBody is the fallback for email clients that don't render HTML.
func plainBody(verifyURL string) string {
	return fmt.Sprintf(
		"Welcome! Please verify your email address by visiting the link below.\n\n"+
			"%s\n\n"+
			"This link expires in 24 hours. If you did not create an account, ignore this email.",
		verifyURL,
	)
}

// htmlBody returns the full HTML email.
func htmlBody(verifyURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0"/>
  <title>Verify your email</title>
</head>
<body style="margin:0;padding:0;background-color:#f4f4f7;font-family:Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f4f4f7;padding:40px 0;">
    <tr>
      <td align="center">
        <table width="600" cellpadding="0" cellspacing="0"
               style="background-color:#ffffff;border-radius:8px;overflow:hidden;
                      box-shadow:0 2px 8px rgba(0,0,0,0.08);max-width:600px;width:100%%;">

          <!-- Header -->
          <tr>
            <td style="background-color:#111827;padding:32px 40px;text-align:center;">
              <h1 style="margin:0;color:#ffffff;font-size:22px;font-weight:700;
                         letter-spacing:-0.5px;">YourApp</h1>
            </td>
          </tr>

          <!-- Body -->
          <tr>
            <td style="padding:40px 40px 24px;">
              <h2 style="margin:0 0 12px;color:#111827;font-size:20px;font-weight:700;">
                Verify your email address
              </h2>
              <p style="margin:0 0 24px;color:#4b5563;font-size:15px;line-height:1.6;">
                Thanks for signing up! Click the button below to verify your email
                address and activate your account. This link expires in
                <strong>24 hours</strong>.
              </p>

              <!-- CTA button -->
              <table cellpadding="0" cellspacing="0" style="margin-bottom:32px;">
                <tr>
                  <td style="background-color:#111827;border-radius:6px;">
                    <a href="%s"
                       style="display:inline-block;padding:14px 32px;color:#ffffff;
                              font-size:15px;font-weight:600;text-decoration:none;
                              border-radius:6px;">
                      Verify my email
                    </a>
                  </td>
                </tr>
              </table>

              <!-- Fallback URL -->
              <p style="margin:0 0 8px;color:#6b7280;font-size:13px;">
                If the button doesn't work, copy and paste this link into your browser:
              </p>
              <p style="margin:0;word-break:break-all;">
                <a href="%s" style="color:#111827;font-size:13px;">%s</a>
              </p>
            </td>
          </tr>

          <!-- Divider -->
          <tr>
            <td style="padding:0 40px;">
              <hr style="border:none;border-top:1px solid #e5e7eb;margin:0;" />
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="padding:24px 40px 32px;">
              <p style="margin:0;color:#9ca3af;font-size:12px;line-height:1.6;">
                If you didn't create an account with YourApp, you can safely ignore
                this email. Someone may have entered your address by mistake.
              </p>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, verifyURL, verifyURL, verifyURL)
}
