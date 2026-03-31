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

// SendInvite sends an org invitation email to the given address.
func (e *Config) SendInvite(toEmail, orgName, inviterName, inviteURL string) error {
	from := mail.NewEmail(e.FromName, e.FromEmail)
	to := mail.NewEmail("", toEmail)

	subject := fmt.Sprintf("You've been invited to join %s", orgName)
	plain := fmt.Sprintf(
		"%s has invited you to join %s on Niute.\n\nAccept your invitation:\n%s\n\nThis link expires in 72 hours.",
		inviterName, orgName, inviteURL,
	)
	html := inviteHTMLBody(orgName, inviterName, inviteURL)

	m := mail.NewSingleEmail(from, subject, to, plain, html)

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

// plainBody is the fallback for email clients that don't render HTML.
func plainBody(verifyURL string) string {
	return fmt.Sprintf(
		"Welcome! Please verify your email address by visiting the link below.\n\n"+
			"%s\n\n"+
			"This link expires in 24 hours. If you did not create an account, ignore this email.",
		verifyURL,
	)
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
    /* Client-safe responsive adjustments */
    @media only screen and (max-width: 600px) {
      .container {
        width: 100%% !important;
      }
      .inner-padding {
        padding-left: 24px !important;
        padding-right: 24px !important;
      }
      .btn {
        padding: 10px 22px !important;
        font-size: 13px !important;
      }
      h1 {
        font-size: 18px !important;
      }
      h2 {
        font-size: 17px !important;
      }
      .body-text {
        font-size: 13px !important;
      }
      .footer-text {
        font-size: 10px !important;
      }
      .link-fallback {
        font-size: 11px !important;
      }
    }
  </style>
</head>
<body style="margin:0; padding:0; background-color:#f5f7fb; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Inter', Roboto, Helvetica, Arial, sans-serif;">
  <!-- Main email wrapper -->
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f5f7fb; padding:32px 0;">
    <tr>
      <td align="center">
        <!-- Main card: slim width 540px for elegant proportions -->
        <table class="container" width="540" cellpadding="0" cellspacing="0" border="0"
               style="max-width:540px; width:100%%; background-color:#ffffff; border-radius:24px; 
                      overflow:hidden; box-shadow:0 8px 20px rgba(0,0,0,0.02), 0 2px 6px rgba(0,0,0,0.03);">
          
          <!-- Header: minimal with brand and subtle accent -->
           <tr>
            <td style="background:#ffffff; padding:24px 32px 8px 32px; border-bottom:1px solid #f0f2f5;">
              <div style="display:flex; align-items:center; gap:8px;">
                <span style="font-size:18px; font-weight:620; letter-spacing:-0.2px; color:#0a0c12;">niute</span>
                <span style="width:4px; height:4px; background:#e2e6ec; border-radius:50%%; display:inline-block;"></span>
                <span style="font-size:12px; font-weight:450; color:#868e9c;">verify</span>
              </div>
             </td>
           </tr>

          <!-- Hero / Icon section (clean and minimal) -->
          <tr>
            <td style="padding:24px 32px 0 32px;">
              <div style="background:#f8fafd; width:40px; height:40px; border-radius:32px; display:flex; align-items:center; justify-content:center;">
                <span style="font-size:20px;">✉️</span>
              </div>
             </td>
          </tr>

          <!-- Main content area -->
          <tr>
            <td class="inner-padding" style="padding:16px 32px 20px 32px;">
              <h2 style="margin:0 0 8px 0; font-size:20px; font-weight:590; letter-spacing:-0.2px; color:#111316; line-height:1.35;">
                Confirm your email
              </h2>
              <p class="body-text" style="margin:0 0 20px 0; font-size:14px; line-height:1.5; color:#4a515e;">
                Almost done. Click the button below to verify your address and activate your Niute account.
                This link expires in <strong style="font-weight:590; color:#111316;">24 hours</strong>.
              </p>
              
              <!-- CTA button: refined, smaller padding and reduced font -->
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

              <!-- Fallback URL with reduced font and cleaner style -->
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

          <!-- Divider -->
          <tr>
            <td style="padding:0 32px;">
              <hr style="border:0; height:1px; background:#f0f2f5; margin:0;">
             </td>
          </tr>

          <!-- Footer: compact and minimal -->
          <tr>
            <td class="inner-padding" style="padding:20px 32px 28px 32px;">
              <p class="footer-text" style="margin:0 0 10px 0; font-size:11px; line-height:1.45; color:#9198a3;">
                If you didn't request this, you can safely ignore this email.
                No changes will be made to your account.
              </p>
              <p class="footer-text" style="margin:0; font-size:10px; color:#a6aebc;">
                Niute — simple & secure
              </p>
             </td>
          </tr>
        </table>
        <!-- subtle security note -->
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
