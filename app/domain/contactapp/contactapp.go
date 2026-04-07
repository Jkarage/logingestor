// Package contactapp maintains the app layer api for the contact domain.
package contactapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jkarage/logingestor/app/sdk/errs"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	mailer       *emailer.Config
	supportEmail string
}

func newApp(mailer *emailer.Config, supportEmail string) *app {
	return &app{
		mailer:       mailer,
		supportEmail: supportEmail,
	}
}

// contact handles POST /v1/contact.
func (a *app) contact(ctx context.Context, r *http.Request) web.Encoder {
	var req ContactRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Subject = strings.TrimSpace(req.Subject)
	req.Message = strings.TrimSpace(req.Message)

	switch {
	case req.Name == "":
		return errs.Errorf(errs.InvalidArgument, "name is required")
	case req.Email == "":
		return errs.Errorf(errs.InvalidArgument, "email is required")
	case req.Subject == "":
		return errs.Errorf(errs.InvalidArgument, "subject is required")
	case req.Message == "":
		return errs.Errorf(errs.InvalidArgument, "message is required")
	}

	if err := a.mailer.SendContactMessage(a.supportEmail, req.Name, req.Email, req.Subject, req.Message); err != nil {
		return errs.Errorf(errs.Internal, "send contact message: %s", err)
	}

	return ContactResponse{Message: fmt.Sprintf("Thanks %s, we'll be in touch soon.", req.Name)}
}
