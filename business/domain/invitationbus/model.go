package invitationbus

import (
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/role"
)

// Invitation represents a pending or accepted org invitation.
type Invitation struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	Email      string
	Role       role.Role
	Token      string
	InvitedBy  uuid.UUID
	ProjectIDs []uuid.UUID
	AcceptedAt *time.Time
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

// NewInvitation contains the information needed to create an invitation.
type NewInvitation struct {
	OrgID      uuid.UUID
	Email      string
	Role       role.Role
	InvitedBy  uuid.UUID
	Token      string
	ProjectIDs []uuid.UUID
	ExpiresAt  time.Time
}
