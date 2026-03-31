// Package invitationapp maintains the app layer api for the invitation domain.
package invitationapp

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	invitationBus invitationbus.ExtBusiness
	userBus       userbus.ExtBusiness
	orgBus        orgbus.ExtBusiness
	projectBus    projectbus.ExtBusiness
	auth          *auth.Auth
	signingKey    string
	mailer        *emailer.Config
	emailBaseURL  string
}

func newApp(
	invitationBus invitationbus.ExtBusiness,
	userBus userbus.ExtBusiness,
	orgBus orgbus.ExtBusiness,
	projectBus projectbus.ExtBusiness,
	ath *auth.Auth,
	signingKey string,
	mailer *emailer.Config,
	emailBaseURL string,
) *app {
	return &app{
		invitationBus: invitationBus,
		userBus:       userBus,
		orgBus:        orgBus,
		projectBus:    projectBus,
		auth:          ath,
		signingKey:    signingKey,
		mailer:        mailer,
		emailBaseURL:  emailBaseURL,
	}
}

// create sends an org invitation.
// POST /v1/orgs/{org_id}/invitations
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var ni NewInvitation
	if err := web.Decode(r, &ni); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busNew, err := toBusNewInvitation(ni)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	actorID := mid.GetSubjectID(ctx)
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	projectIDStrs := make([]string, len(busNew.ProjectIDs))
	for i, id := range busNew.ProjectIDs {
		projectIDStrs[i] = id.String()
	}

	// Generate the signed invite JWT. Lookup is always done by token string, so
	// we don't need to embed the DB row ID in the claims.
	token, err := a.auth.GenerateInviteToken(a.signingKey, auth.InviteClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   actorID.String(),
			Issuer:    a.auth.Issuer(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
		Type:       auth.InviteTokenType,
		OrgID:      orgID.String(),
		Email:      busNew.Email,
		Role:       busNew.Role.String(),
		ProjectIDs: projectIDStrs,
	})
	if err != nil {
		return errs.Errorf(errs.Internal, "generate invite token: %s", err)
	}

	busNew.OrgID = orgID
	busNew.InvitedBy = actorID
	busNew.Token = token
	busNew.ExpiresAt = expiresAt

	inv, err := a.invitationBus.Create(ctx, actorID, busNew)
	if err != nil {
		return errs.Errorf(errs.Internal, "create invitation: %s", err)
	}

	// Resolve names for the email.
	org, err := a.orgBus.QueryByID(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryorg: %s", err)
	}

	inviter, err := a.userBus.QueryByID(ctx, actorID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryinviter: %s", err)
	}

	inviteLink := a.emailBaseURL + "/invite?token=" + token

	if err := a.mailer.SendInvite(busNew.Email, org.Name.String(), inviter.Name.String(), inviteLink); err != nil {
		return errs.Errorf(errs.Internal, "send invite email: %s", err)
	}

	return toAppInvitation(inv)
}

// accept processes an invitation token.
// POST /v1/invitations/accept
func (a *app) accept(ctx context.Context, r *http.Request) web.Encoder {
	var body AcceptInvitation
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	if body.Token == "" {
		return errs.New(errs.InvalidArgument, errors.New("token is required"))
	}

	// Verify the invite JWT signature, expiry, and type claim.
	claims, err := a.auth.ParseInviteToken(ctx, body.Token)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	// Load the invitation row to confirm it hasn't been revoked or already accepted.
	inv, err := a.invitationBus.QueryByToken(ctx, body.Token)
	if err != nil {
		if errors.Is(err, invitationbus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("invitation not found or revoked"))
		}
		return errs.Errorf(errs.Internal, "querybytoken: %s", err)
	}

	if inv.AcceptedAt != nil {
		return errs.New(errs.Aborted, invitationbus.ErrAlreadyUsed)
	}

	if time.Now().After(inv.ExpiresAt) {
		return errs.New(errs.Aborted, invitationbus.ErrExpired)
	}

	// Check whether the invitee already has an account.
	emailAddr := mail.Address{Address: claims.Email}
	usr, err := a.userBus.QueryByEmail(ctx, emailAddr)
	if err != nil {
		if errors.Is(err, userbus.ErrNotFound) {
			// No account yet — tell the frontend to show the sign-up form.
			// The frontend stores the token in localStorage, registers the user,
			// verifies their email, then calls this endpoint again.
			return AcceptResult{
				Status: "signup_required",
				OrgID:  inv.OrgID.String(),
				Email:  inv.Email,
				Token:  body.Token,
			}
		}
		return errs.Errorf(errs.Internal, "querybyemail: %s", err)
	}

	// User exists — add them to the org with the invited role.
	if _, err := a.orgBus.AddMember(ctx, usr.ID, orgbus.NewOrgMember{
		OrgID:  inv.OrgID,
		UserID: usr.ID,
		Role:   inv.Role,
	}); err != nil {
		if !errors.Is(err, orgbus.ErrMemberExists) {
			return errs.Errorf(errs.Internal, "addmember: %s", err)
		}
		// Already a member — still grant any additional project access below.
	}

	// Grant project-level access for PROJECT MANAGER / VIEWER roles.
	for _, projectID := range inv.ProjectIDs {
		if err := a.projectBus.GrantProjectAccess(ctx, usr.ID, usr.ID, projectID); err != nil {
			return errs.Errorf(errs.Internal, "grantprojectaccess projectID[%s]: %s", projectID, err)
		}
	}

	// Mark the invitation as accepted.
	if err := a.invitationBus.MarkAccepted(ctx, inv.ID, time.Now()); err != nil {
		return errs.Errorf(errs.Internal, "markaccepted: %s", err)
	}

	return AcceptResult{
		Status: "joined",
		OrgID:  inv.OrgID.String(),
	}
}

// revoke deletes a pending invitation.
// DELETE /v1/orgs/{org_id}/invitations/{invitation_id}
func (a *app) revoke(ctx context.Context, r *http.Request) web.Encoder {
	invID, err := uuid.Parse(web.Param(r, "invitation_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	actorID := mid.GetSubjectID(ctx)

	if err := a.invitationBus.Revoke(ctx, actorID, invID); err != nil {
		switch {
		case errors.Is(err, invitationbus.ErrNotFound):
			return errs.New(errs.NotFound, err)
		case errors.Is(err, invitationbus.ErrAlreadyUsed):
			return errs.New(errs.Aborted, err)
		}
		return errs.Errorf(errs.Internal, "revoke: invID[%s]: %s", invID, err)
	}

	return nil
}

// query lists all invitations for an org.
// GET /v1/orgs/{org_id}/invitations
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	invs, err := a.invitationBus.QueryByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	return toAppInvitations(invs)
}
