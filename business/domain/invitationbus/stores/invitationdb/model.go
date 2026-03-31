package invitationdb

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
	"github.com/jkarage/logingestor/business/types/role"
)

// invitationDB is the database representation of an org_invitations row.
type invitationDB struct {
	ID         uuid.UUID      `db:"id"`
	OrgID      uuid.UUID      `db:"org_id"`
	Email      string         `db:"email"`
	Role       string         `db:"role"`
	Token      string         `db:"token"`
	InvitedBy  uuid.UUID      `db:"invited_by"`
	ProjectIDs dbarray.String `db:"project_ids"`
	AcceptedAt sql.NullTime   `db:"accepted_at"`
	ExpiresAt  time.Time      `db:"expires_at"`
	CreatedAt  time.Time      `db:"created_at"`
}

func toDBInvitation(bus invitationbus.Invitation) invitationDB {
	projectIDs := make(dbarray.String, len(bus.ProjectIDs))
	for i, id := range bus.ProjectIDs {
		projectIDs[i] = id.String()
	}

	var acceptedAt sql.NullTime
	if bus.AcceptedAt != nil {
		acceptedAt = sql.NullTime{Time: bus.AcceptedAt.UTC(), Valid: true}
	}

	return invitationDB{
		ID:         bus.ID,
		OrgID:      bus.OrgID,
		Email:      bus.Email,
		Role:       bus.Role.String(),
		Token:      bus.Token,
		InvitedBy:  bus.InvitedBy,
		ProjectIDs: projectIDs,
		AcceptedAt: acceptedAt,
		ExpiresAt:  bus.ExpiresAt.UTC(),
		CreatedAt:  bus.CreatedAt.UTC(),
	}
}

func toBusInvitation(db invitationDB) (invitationbus.Invitation, error) {
	r, err := role.Parse(db.Role)
	if err != nil {
		return invitationbus.Invitation{}, fmt.Errorf("parse role: %w", err)
	}

	projectIDs := make([]uuid.UUID, 0, len(db.ProjectIDs))
	for _, s := range db.ProjectIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return invitationbus.Invitation{}, fmt.Errorf("parse project_id %q: %w", s, err)
		}
		projectIDs = append(projectIDs, id)
	}

	var acceptedAt *time.Time
	if db.AcceptedAt.Valid {
		t := db.AcceptedAt.Time.In(time.Local)
		acceptedAt = &t
	}

	return invitationbus.Invitation{
		ID:         db.ID,
		OrgID:      db.OrgID,
		Email:      db.Email,
		Role:       r,
		Token:      db.Token,
		InvitedBy:  db.InvitedBy,
		ProjectIDs: projectIDs,
		AcceptedAt: acceptedAt,
		ExpiresAt:  db.ExpiresAt.In(time.Local),
		CreatedAt:  db.CreatedAt.In(time.Local),
	}, nil
}

func toBusInvitations(dbs []invitationDB) ([]invitationbus.Invitation, error) {
	invs := make([]invitationbus.Invitation, len(dbs))
	for i, db := range dbs {
		var err error
		invs[i], err = toBusInvitation(db)
		if err != nil {
			return nil, err
		}
	}
	return invs, nil
}
