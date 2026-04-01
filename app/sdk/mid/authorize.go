package mid

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

// ErrInvalidID represents a condition where the id is not a uuid.
var ErrInvalidID = errors.New("ID is not in its proper form")

// Authorize validates authorization via the auth service.
func Authorize(client authclient.Authenticator, rule string) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			userID, err := GetUserID(ctx)
			if err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			auth := authclient.Authorize{
				Claims: GetClaims(ctx),
				UserID: userID,
				Rule:   rule,
			}

			// ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			// defer cancel()

			if err := client.Authorize(ctx, auth); err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			return next(ctx, r)
		}

		return h
	}

	return m
}

// AuthorizeOrgMember checks that the authenticated user is a member of the
// organization specified by the {org_id} path parameter. Routes without that
// parameter pass through unchanged. Super admins bypass the membership check.
func AuthorizeOrgMember(orgBus orgbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			rawID := web.Param(r, "org_id")
			if rawID == "" {
				return next(ctx, r)
			}

			orgID, err := uuid.Parse(rawID)
			if err != nil {
				return errs.New(errs.InvalidArgument, ErrInvalidID)
			}

			// Super admins have system-wide access.
			claims := GetClaims(ctx)
			for _, claimRole := range claims.Roles {
				if claimRole == role.Admin.String() {
					return next(ctx, r)
				}
			}

			userID, err := GetUserID(ctx)
			if err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			// ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			// defer cancel()

			userOrgs, err := orgBus.QueryByUserID(ctx, userID)
			if err != nil {
				return errs.Errorf(errs.Internal, "authorizeorgmember: userID[%s]: %s", userID, err)
			}

			for _, uo := range userOrgs {
				if uo.ID == orgID {
					return next(ctx, r)
				}
			}

			return errs.New(errs.PermissionDenied, errors.New("user is not a member of this organization"))
		}

		return h
	}

	return m
}

// AuthorizeUser executes the specified role and extracts the specified
// user from the DB if a user id is specified in the call. Depending on the rule
// specified, the userid from the claims may be compared with the specified
// user id.
func AuthorizeUser(client authclient.Authenticator, userBus userbus.ExtBusiness, rule string) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			id := web.Param(r, "user_id")

			var userID uuid.UUID

			if id != "" {
				var err error
				userID, err = uuid.Parse(id)
				if err != nil {
					return errs.New(errs.Unauthenticated, ErrInvalidID)
				}

				usr, err := userBus.QueryByID(ctx, userID)
				if err != nil {
					switch {
					case errors.Is(err, userbus.ErrNotFound):
						return errs.New(errs.Unauthenticated, err)
					default:
						return errs.Errorf(errs.Unauthenticated, "querybyid: userID[%s]: %s", userID, err)
					}
				}

				ctx = setUser(ctx, usr)
			}

			// ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			// defer cancel()

			auth := authclient.Authorize{
				Claims: GetClaims(ctx),
				UserID: userID,
				Rule:   rule,
			}

			if err := client.Authorize(ctx, auth); err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			return next(ctx, r)
		}

		return h
	}

	return m
}
