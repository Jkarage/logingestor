package invitationapp

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/types/role"
)

// =============================================================================
// Invitation response shape

// Invitation is the API representation of an org_invitation row.
type Invitation struct {
	ID         string   `json:"id"`
	OrgID      string   `json:"orgId"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	InvitedBy  string   `json:"invitedBy"`
	ProjectIDs []string `json:"projectIds"`
	AcceptedAt *string  `json:"acceptedAt"`
	ExpiresAt  string   `json:"expiresAt"`
	CreatedAt  string   `json:"createdAt"`
}

// Encode implements the web.Encoder interface.
func (app Invitation) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Invitations is a slice of Invitation that implements web.Encoder.
type Invitations []Invitation

// Encode implements the web.Encoder interface.
func (app Invitations) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppInvitation(bus invitationbus.Invitation) Invitation {
	projectIDs := make([]string, len(bus.ProjectIDs))
	for i, id := range bus.ProjectIDs {
		projectIDs[i] = id.String()
	}

	var acceptedAt *string
	if bus.AcceptedAt != nil {
		s := bus.AcceptedAt.Format(time.RFC3339)
		acceptedAt = &s
	}

	return Invitation{
		ID:         bus.ID.String(),
		OrgID:      bus.OrgID.String(),
		Email:      bus.Email,
		Role:       bus.Role.String(),
		InvitedBy:  bus.InvitedBy.String(),
		ProjectIDs: projectIDs,
		AcceptedAt: acceptedAt,
		ExpiresAt:  bus.ExpiresAt.Format(time.RFC3339),
		CreatedAt:  bus.CreatedAt.Format(time.RFC3339),
	}
}

func toAppInvitations(invs []invitationbus.Invitation) Invitations {
	app := make(Invitations, len(invs))
	for i, inv := range invs {
		app[i] = toAppInvitation(inv)
	}
	return app
}

// =============================================================================
// Create request

// NewInvitation is the request body for sending an invitation.
type NewInvitation struct {
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	ProjectIDs []string `json:"projectIds"`
}

// Decode implements the web.Decoder interface.
func (app *NewInvitation) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusNewInvitation(app NewInvitation) (invitationbus.NewInvitation, error) {
	var fieldErrors errs.FieldErrors

	if _, err := mail.ParseAddress(app.Email); err != nil {
		fieldErrors.Add("email", fmt.Errorf("must be a valid email address"))
	}

	r, err := role.Parse(app.Role)
	if err != nil {
		fieldErrors.Add("role", fmt.Errorf("must be one of: ORG ADMIN, PROJECT MANAGER, VIEWER"))
	}

	projectIDs := make([]uuid.UUID, 0, len(app.ProjectIDs))
	for _, s := range app.ProjectIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			fieldErrors.Add("projectIds", fmt.Errorf("invalid UUID %q", s))
			break
		}
		projectIDs = append(projectIDs, id)
	}

	if len(fieldErrors) > 0 {
		return invitationbus.NewInvitation{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return invitationbus.NewInvitation{
		Email:      app.Email,
		Role:       r,
		ProjectIDs: projectIDs,
	}, nil
}

// =============================================================================
// Accept request / response

// AcceptInvitation is the request body for accepting an invitation.
type AcceptInvitation struct {
	Token string `json:"token"`
}

// Decode implements the web.Decoder interface.
func (app *AcceptInvitation) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// AcceptResult is the response body for the accept endpoint.
// Status is either "joined" or "signup_required".
type AcceptResult struct {
	Status string `json:"status"`
	OrgID  string `json:"orgId,omitempty"`
	Email  string `json:"email,omitempty"`
	// Token is only present when status == "signup_required" so the frontend
	// can persist it through the sign-up flow and call accept again.
	Token string `json:"token,omitempty"`
}

// Encode implements the web.Encoder interface.
func (app AcceptResult) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}
