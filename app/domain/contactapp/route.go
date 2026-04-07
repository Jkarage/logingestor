package contactapp

import (
	"net/http"

	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Mailer       *emailer.Config
	SupportEmail string
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	a := newApp(cfg.Mailer, cfg.SupportEmail)

	app.HandlerFuncNoMid(http.MethodPost, version, "/contact", a.contact)
}
