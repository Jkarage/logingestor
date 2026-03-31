// Package userapp maintains the app layer api for the user domain.
package userapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/app/sdk/query"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	emailBaseURL string
	userBus      userbus.ExtBusiness
	auth         *auth.Auth
	mailer       *emailer.Config
	signingKey   string
}

func newApp(emailBaseURL, signingKey string, mailer *emailer.Config, userBus userbus.ExtBusiness, auth *auth.Auth) *app {
	return &app{
		userBus:      userBus,
		auth:         auth,
		emailBaseURL: emailBaseURL,
		signingKey:   signingKey,
		mailer:       mailer,
	}
}

func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var app NewUser
	if err := web.Decode(r, &app); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// If an invite token is present, validate it before creating the user.
	// The email in the token must match the email in the request body.
	if app.InviteToken != "" {
		claims, err := a.auth.ParseInviteToken(context.Background(), app.InviteToken)
		if err != nil {
			return errs.New(errs.Unauthenticated, err)
		}
		if claims.Email != app.Email {
			return errs.New(errs.InvalidArgument, errors.New("email does not match invite"))
		}
	}

	nc, err := toBusNewUser(app)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// Create user — enabled=false inside userbus.Create
	usr, err := a.userBus.Create(ctx, mid.GetSubjectID(ctx), nc)
	if err != nil {
		if errors.Is(err, userbus.ErrUniqueEmail) {
			return errs.New(errs.Aborted, userbus.ErrUniqueEmail)
		}
		return errs.Errorf(errs.Internal, "create: usr[%+v]: %s", usr, err)
	}

	// Invite path: activate immediately, no email confirmation needed.
	// The frontend calls POST /v1/invitations/accept with the same token
	// after this to complete the org join.
	if app.InviteToken != "" {
		if err := a.userBus.Activate(ctx, usr.ID); err != nil {
			return errs.Errorf(errs.Internal, "activate invited user: %s", err)
		}
		usr.Enabled = true
		return toAppUser(usr)
	}

	// Normal path: generate a verify token and send confirmation email.
	token, err := a.auth.GenerateToken(a.signingKey, auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   usr.ID.String(),
			Issuer:    a.auth.Issuer(),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
	})
	if err != nil {
		return errs.New(errs.Internal, err)
	}

	link := a.emailBaseURL + "/verify?token=" + token

	if err := a.mailer.SendVerification(usr.Email.Address, usr.Name.String(), link); err != nil {
		return errs.New(errs.Internal, err)
	}

	return toAppUser(usr)
}

func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	var app UpdateUser
	if err := web.Decode(r, &app); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	uu, err := toBusUpdateUser(app)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	usr, err := mid.GetUser(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "user missing in context: %s", err)
	}

	updUsr, err := a.userBus.Update(ctx, mid.GetSubjectID(ctx), usr, uu)
	if err != nil {
		return errs.Errorf(errs.Internal, "update: userID[%s] uu[%+v]: %s", usr.ID, uu, err)
	}

	return toAppUser(updUsr)
}

func (a *app) updateRole(ctx context.Context, r *http.Request) web.Encoder {
	var app UpdateUserRole
	if err := web.Decode(r, &app); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	uu, err := toBusUpdateUserRole(app)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	usr, err := mid.GetUser(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "user missing in context: %s", err)
	}

	updUsr, err := a.userBus.Update(ctx, mid.GetSubjectID(ctx), usr, uu)
	if err != nil {
		return errs.Errorf(errs.Internal, "updaterole: userID[%s] uu[%+v]: %s", usr.ID, uu, err)
	}

	return toAppUser(updUsr)
}

func (a *app) delete(ctx context.Context, _ *http.Request) web.Encoder {
	usr, err := mid.GetUser(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "userID missing in context: %s", err)
	}

	if err := a.userBus.Delete(ctx, mid.GetSubjectID(ctx), usr); err != nil {
		return errs.Errorf(errs.Internal, "delete: userID[%s]: %s", usr.ID, err)
	}

	return nil
}

func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	qp, err := parseQueryParams(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	page, err := page.Parse(qp.Page, qp.Rows)
	if err != nil {
		return errs.NewFieldErrors("page", err)
	}

	filter, err := parseFilter(qp)
	if err != nil {
		return err.(*errs.Error)
	}

	orderBy, err := order.Parse(orderByFields, qp.OrderBy, userbus.DefaultOrderBy)
	if err != nil {
		return errs.NewFieldErrors("order", err)
	}

	usrs, err := a.userBus.Query(ctx, filter, orderBy, page)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	total, err := a.userBus.Count(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "count: %s", err)
	}

	return query.NewResult(toAppUsers(usrs), total, page)
}

func (a *app) queryMe(ctx context.Context, _ *http.Request) web.Encoder {
	userID := mid.GetSubjectID(ctx)

	usr, err := a.userBus.QueryByID(ctx, userID)
	if err != nil {
		if errors.Is(err, userbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "queryme: userID[%s]: %s", userID, err)
	}

	return toAppUser(usr)
}

func (a *app) queryByID(ctx context.Context, _ *http.Request) web.Encoder {
	usr, err := mid.GetUser(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyid: %s", err)
	}

	return toAppUser(usr)
}

func (a *app) verify(ctx context.Context, r *http.Request) web.Encoder {
	var cu ConfirmUser
	if err := web.Decode(r, &cu); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// Authenticate parses the token, checks the signature using the public key
	// matched by kid in the header, validates expiry + issuer, returns Claims.
	claims, err := a.auth.ParseVerifyToken(context.Background(), cu.Token)
	if err != nil {
		return errs.New(errs.Internal, err)
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// Flip enabled=true in DB
	if err := a.userBus.Activate(ctx, userID); err != nil {
		return errs.New(errs.Internal, err)
	}

	return nil
}
