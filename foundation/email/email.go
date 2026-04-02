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
		Subject: "Verify your email address",
		Html:    htmlBody(verifyURL),
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}

	return nil
}

// SendInvite sends an org invitation email to the given address.
func (e *Config) SendInvite(toEmail, orgName, inviterName, inviteURL string) error {
	client := resend.NewClient(e.APIKey)

	subject := fmt.Sprintf("You've been invited to join %s", orgName)

	_, err := client.Emails.Send(&resend.SendEmailRequest{
		From:    e.from(),
		To:      []string{toEmail},
		Subject: subject,
		Html:    inviteHTMLBody(orgName, inviterName, inviteURL),
	})
	if err != nil {
		return fmt.Errorf("resend send: %w", err)
	}

	return nil
}

func inviteHTMLBody(orgName, inviterName, inviteURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Invitation to %s | Niute</title>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Inter', Roboto, Helvetica, Arial, sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f5f7fb; padding:32px 0;">
    <tr>
      <td align="center">
        <table width="540" cellpadding="0" cellspacing="0" border="0"
               style="max-width:540px; width:100%%; background-color:#ffffff; border-radius:24px;
                      overflow:hidden; box-shadow:0 8px 20px rgba(0,0,0,0.02), 0 2px 6px rgba(0,0,0,0.03);">
          <tr>
            <td style="background:#ffffff; padding:24px 32px 8px 32px; border-bottom:1px solid #f0f2f5;">
              <span style="font-size:18px; font-weight:620; letter-spacing:-0.2px; color:#0a0c12;">niute</span>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 32px 0 32px;">
              <div style="background:#f8fafd; width:40px; height:40px; border-radius:32px; display:flex; align-items:center; justify-content:center;">
                <span style="font-size:20px;">🤝</span>
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:16px 32px 20px 32px;">
              <h2 style="margin:0 0 8px 0; font-size:20px; font-weight:590; letter-spacing:-0.2px; color:#111316; line-height:1.35;">
                You're invited to join %s
              </h2>
              <p style="margin:0 0 20px 0; font-size:14px; line-height:1.5; color:#4a515e;">
                <strong style="color:#111316;">%s</strong> has invited you to collaborate on <strong style="color:#111316;">%s</strong>.
                Click below to accept your invitation. This link expires in <strong style="font-weight:590; color:#111316;">72 hours</strong>.
              </p>
              <table cellpadding="0" cellspacing="0" border="0" style="margin: 6px 0 28px 0;">
                <tr>
                  <td align="center" style="background:#0a0c12; border-radius:40px;">
                    <a href="%s" style="display:inline-block; padding:10px 26px; background:#0a0c12; color:#ffffff;
                                  font-size:13px; font-weight:500; text-decoration:none; border-radius:40px;
                                  letter-spacing:-0.1px; border:0;">
                      Accept invitation →
                    </a>
                  </td>
                </tr>
              </table>
              <div style="border-top:1px solid #f0f2f5; margin-top:8px; padding-top:20px;">
                <p style="margin:0 0 5px 0; font-size:11px; color:#848c9a;">🔗 Button not working? Copy this link:</p>
                <p style="margin:0; word-break:break-all;">
                  <a href="%s" style="font-size:11px; font-family: 'SF Mono', monospace; color:#2c5f8a; text-decoration:underline;">%s</a>
                </p>
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:0 32px;"><hr style="border:0; height:1px; background:#f0f2f5; margin:0;"></td>
          </tr>
          <tr>
            <td style="padding:20px 32px 28px 32px;">
              <p style="margin:0; font-size:11px; line-height:1.45; color:#9198a3;">
                If you weren't expecting this invitation, you can safely ignore this email.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, orgName, orgName, inviterName, orgName, inviteURL, inviteURL, inviteURL)
}

// htmlBody returns the full HTML email with modern, minimal design and reduced font sizes.
func htmlBody(verifyURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verify your email | Niute</title>
  <style>
    @media only screen and (max-width: 600px) {
      .container { width: 100%% !important; }
      .inner-padding { padding-left: 24px !important; padding-right: 24px !important; }
      .btn { padding: 10px 22px !important; font-size: 13px !important; }
      h1 { font-size: 18px !important; }
      h2 { font-size: 17px !important; }
      .body-text { font-size: 13px !important; }
      .footer-text { font-size: 10px !important; }
      .link-fallback { font-size: 11px !important; }
    }
  </style>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Inter', Roboto, Helvetica, Arial, sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f5f7fb; padding:32px 0;">
    <tr>
      <td align="center">
        <table class="container" width="540" cellpadding="0" cellspacing="0" border="0"
               style="max-width:540px; width:100%%; background-color:#ffffff; border-radius:24px;
                      overflow:hidden; box-shadow:0 8px 20px rgba(0,0,0,0.02), 0 2px 6px rgba(0,0,0,0.03);">
          <tr>
            <td style="background:#ffffff; padding:24px 32px 8px 32px; border-bottom:1px solid #f0f2f5;">
              <div style="display:flex; align-items:center; gap:8px;">
                <span style="font-size:18px; font-weight:620; letter-spacing:-0.2px; color:#0a0c12;">niute</span>
                <span style="width:4px; height:4px; background:#e2e6ec; border-radius:50%%; display:inline-block;"></span>
                <span style="font-size:12px; font-weight:450; color:#868e9c;">verify</span>
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 32px 0 32px;">
              <div style="background:#f8fafd; width:40px; height:40px; border-radius:32px; display:flex; align-items:center; justify-content:center;">
                <span style="font-size:20px;">✉️</span>
              </div>
            </td>
          </tr>
          <tr>
            <td class="inner-padding" style="padding:16px 32px 20px 32px;">
              <h2 style="margin:0 0 8px 0; font-size:20px; font-weight:590; letter-spacing:-0.2px; color:#111316; line-height:1.35;">
                Confirm your email
              </h2>
              <p class="body-text" style="margin:0 0 20px 0; font-size:14px; line-height:1.5; color:#4a515e;">
                Almost done. Click the button below to verify your address and activate your Niute account.
                This link expires in <strong style="font-weight:590; color:#111316;">24 hours</strong>.
              </p>
              <table cellpadding="0" cellspacing="0" border="0" style="margin: 6px 0 28px 0;">
                <tr>
                  <td align="center" style="background:#0a0c12; border-radius:40px;">
                    <a href="%s" class="btn" style="display:inline-block; padding:10px 26px; background:#0a0c12; color:#ffffff;
                                  font-size:13px; font-weight:500; text-decoration:none; border-radius:40px;
                                  letter-spacing:-0.1px; border:0;">
                      Verify email →
                    </a>
                  </td>
                </tr>
              </table>
              <div style="border-top:1px solid #f0f2f5; margin-top:8px; padding-top:20px;">
                <p style="margin:0 0 5px 0; font-size:11px; color:#848c9a; letter-spacing:-0.1px;">
                  🔗 Button not working? Copy this link:
                </p>
                <p style="margin:0; word-break:break-all;">
                  <a href="%s" class="link-fallback" style="font-size:11px; font-family: 'SF Mono', monospace; color:#2c5f8a; text-decoration:underline; word-break:break-all;">%s</a>
                </p>
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:0 32px;">
              <hr style="border:0; height:1px; background:#f0f2f5; margin:0;">
            </td>
          </tr>
          <tr>
            <td class="inner-padding" style="padding:20px 32px 28px 32px;">
              <p class="footer-text" style="margin:0 0 10px 0; font-size:11px; line-height:1.45; color:#9198a3;">
                If you didn't request this, you can safely ignore this email.
                No changes will be made to your account.
              </p>
              <p class="footer-text" style="margin:0; font-size:10px; color:#a6aebc;">
                Niute — simple &amp; secure
              </p>
            </td>
          </tr>
        </table>
        <table width="540" style="max-width:540px; width:100%%; margin-top:16px;">
          <tr>
            <td align="center" style="font-size:10px; color:#9ba2af; padding:0 20px; letter-spacing:0.2px;">
              Secure link • expires in 24 hours
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, verifyURL, verifyURL, verifyURL)
}
