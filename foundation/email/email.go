// foundation/emailer/emailer.go
package email

import "net/smtp"

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

type Emailer struct{ cfg Config }

func New(cfg Config) *Emailer { return &Emailer{cfg: cfg} }

func (e *Emailer) SendVerification(toAddr, verifyURL string) error {
	msg := []byte(
		"Subject: Verify your email\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
			"Click the link below to verify your account:\r\n\r\n" +
			verifyURL + "\r\n\r\nThis link expires in 24 hours.",
	)
	addr := e.cfg.Host + ":" + e.cfg.Port
	auth := smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)
	return smtp.SendMail(addr, auth, e.cfg.From, []string{toAddr}, msg)
}
