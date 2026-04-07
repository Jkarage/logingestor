package emailer

import (
	"fmt"

	"github.com/resend/resend-go/v3"
)

// Config holds Resend configuration.
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

func (e *Config) from() string {
	return fmt.Sprintf("%s <%s>", e.FromName, e.FromEmail)
}

// SendVerification sends an HTML verification email to the given address.
func (e *Config) SendVerification(toEmail, toName, verifyURL string) error {
	client := resend.NewClient(e.APIKey)

	_, err := client.Emails.Send(&resend.SendEmailRequest{
		From:    e.from(),
		To:      []string{toEmail},
		Subject: "Verify your email address – Streamlogia",
		Html:    verifyHTMLBody(verifyURL),
		Text:    verifyPlainBody(verifyURL),
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}

	return nil
}

// SendInvite sends an org invitation email to the given address.
func (e *Config) SendInvite(toEmail, orgName, inviterName, inviteURL string) error {
	client := resend.NewClient(e.APIKey)

	subject := fmt.Sprintf("%s invited you to join %s on Streamlogia", inviterName, orgName)

	_, err := client.Emails.Send(&resend.SendEmailRequest{
		From:    e.from(),
		To:      []string{toEmail},
		Subject: subject,
		Html:    inviteHTMLBody(orgName, inviterName, inviteURL),
		Text:    invitePlainBody(orgName, inviterName, inviteURL),
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}

	return nil
}

// SendContactMessage forwards a customer contact form submission to the support inbox.
// The customer's email is set as Reply-To so the team can reply directly to them.
func (e *Config) SendContactMessage(supportEmail, fromName, fromEmail, subject, message string) error {
	client := resend.NewClient(e.APIKey)

	_, err := client.Emails.Send(&resend.SendEmailRequest{
		From:    e.from(),
		To:      []string{supportEmail},
		ReplyTo: fmt.Sprintf("%s <%s>", fromName, fromEmail),
		Subject: fmt.Sprintf("[Contact] %s", subject),
		Text:    fmt.Sprintf("From: %s <%s>\n\n%s", fromName, fromEmail, message),
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}

	return nil
}

// =============================================================================
// Invite email

func invitePlainBody(orgName, inviterName, inviteURL string) string {
	return fmt.Sprintf(
		"%s has invited you to join %s on Streamlogia.\n\n"+
			"Accept your invitation:\n%s\n\n"+
			"This link expires in 1 hour.\n\n"+
			"If you weren't expecting this invitation, you can safely ignore this email.\n\n"+
			"– The Streamlogia team",
		inviterName, orgName, inviteURL,
	)
}

func inviteHTMLBody(orgName, inviterName, inviteURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s invited you to join %s</title>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f5f7fb; padding:32px 0;">
    <tr>
      <td align="center">
        <table width="540" cellpadding="0" cellspacing="0" border="0"
               style="max-width:540px; width:100%%; background-color:#ffffff; border-radius:16px;
                      overflow:hidden; box-shadow:0 4px 12px rgba(0,0,0,0.06);">

          <!-- Header -->
          <tr>
            <td style="background:#ffffff; padding:24px 32px 16px 32px; border-bottom:1px solid #f0f2f5;">
              <span style="font-size:17px; font-weight:700; letter-spacing:-0.3px; color:#0a0c12;">Streamlogia</span>
            </td>
          </tr>

          <!-- Body -->
          <tr>
            <td style="padding:28px 32px 8px 32px;">
              <h2 style="margin:0 0 10px 0; font-size:20px; font-weight:600; color:#111316; line-height:1.35;">
                You've been invited to join %s
              </h2>
              <p style="margin:0 0 24px 0; font-size:14px; line-height:1.6; color:#4a515e;">
                <strong style="color:#111316;">%s</strong> has invited you to collaborate on
                <strong style="color:#111316;">%s</strong> on Streamlogia.
                Accept below — this invitation expires in <strong style="color:#111316;">72 hours</strong>.
              </p>

              <!-- CTA -->
              <table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:28px;">
                <tr>
                  <td style="background:#0a0c12; border-radius:8px;">
                    <a href="%s"
                       style="display:inline-block; padding:12px 28px; color:#ffffff;
                              font-size:14px; font-weight:600; text-decoration:none; border-radius:8px;">
                      Accept invitation
                    </a>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="padding:0 32px 24px 32px; border-top:1px solid #f0f2f5;">
              <p style="margin:16px 0 0 0; font-size:12px; line-height:1.5; color:#9198a3;">
                If you weren't expecting this invitation, you can safely ignore this email.
                You won't be added to any organisation unless you click the link above.
              </p>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, inviterName, orgName, orgName, inviterName, orgName, inviteURL)
}

// =============================================================================
// Verification email

func verifyPlainBody(verifyURL string) string {
	return fmt.Sprintf(
		"Welcome to Streamlogia!\n\n"+
			"Please verify your email address by visiting the link below:\n%s\n\n"+
			"This link expires in 24 hours.\n\n"+
			"If you did not create an account, you can safely ignore this email.\n\n"+
			"– The Streamlogia team",
		verifyURL,
	)
}

func verifyHTMLBody(verifyURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verify your email – Streamlogia</title>
  <style>
    @media only screen and (max-width: 600px) {
      .container { width: 100%% !important; }
      .inner-padding { padding-left: 24px !important; padding-right: 24px !important; }
    }
  </style>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f5f7fb; padding:32px 0;">
    <tr>
      <td align="center">
        <table class="container" width="540" cellpadding="0" cellspacing="0" border="0"
               style="max-width:540px; width:100%%; background-color:#ffffff; border-radius:16px;
                      overflow:hidden; box-shadow:0 4px 12px rgba(0,0,0,0.06);">

          <!-- Header -->
          <tr>
            <td style="background:#ffffff; padding:24px 32px 16px 32px; border-bottom:1px solid #f0f2f5;">
              <span style="font-size:17px; font-weight:700; letter-spacing:-0.3px; color:#0a0c12;">Streamlogia</span>
            </td>
          </tr>

          <!-- Body -->
          <tr>
            <td class="inner-padding" style="padding:28px 32px 8px 32px;">
              <h2 style="margin:0 0 10px 0; font-size:20px; font-weight:600; color:#111316; line-height:1.35;">
                Confirm your email address
              </h2>
              <p style="margin:0 0 24px 0; font-size:14px; line-height:1.6; color:#4a515e;">
                Thanks for signing up for Streamlogia. Click the button below to verify your
                email address and activate your account.
                This link expires in <strong style="color:#111316;">24 hours</strong>.
              </p>

              <!-- CTA -->
              <table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:28px;">
                <tr>
                  <td style="background:#0a0c12; border-radius:8px;">
                    <a href="%s"
                       style="display:inline-block; padding:12px 28px; color:#ffffff;
                              font-size:14px; font-weight:600; text-decoration:none; border-radius:8px;">
                      Verify email address
                    </a>
                  </td>
                </tr>
              </table>
            </td>
          </tr>
          <!-- Footer -->
          <tr>
            <td class="inner-padding" style="padding:0 32px 24px 32px; border-top:1px solid #f0f2f5;">
              <p style="margin:16px 0 0 0; font-size:12px; line-height:1.5; color:#9198a3;">
                If you didn't request this, you can safely ignore this email.
                No changes will be made to your account.
              </p>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, verifyURL)
}
