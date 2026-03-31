// Package invitationdb contains org invitation related CRUD functionality.
package invitationdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for invitation database access.
type Store struct {
	log *logger.Logger
	db  sqlx.ExtContext
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// NewWithTx constructs a new Store value replacing the sqlx DB
// value with a sqlx DB value that is currently inside a transaction.
func (s *Store) NewWithTx(tx sqldb.CommitRollbacker) (invitationbus.Storer, error) {
	ec, err := sqldb.GetExtContext(tx)
	if err != nil {
		return nil, err
	}
	return &Store{log: s.log, db: ec}, nil
}

// Create inserts a new invitation into the database.
func (s *Store) Create(ctx context.Context, inv invitationbus.Invitation) error {
	const q = `
	INSERT INTO org_invitations
		(id, org_id, email, role, token, invited_by, project_ids, accepted_at, expires_at, created_at)
	VALUES
		(:id, :org_id, :email, :role, :token, :invited_by, :project_ids, :accepted_at, :expires_at, :created_at)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBInvitation(inv)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}
	return nil
}

// QueryByID gets the specified invitation from the database.
func (s *Store) QueryByID(ctx context.Context, invID uuid.UUID) (invitationbus.Invitation, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: invID.String()}

	const q = `
	SELECT id, org_id, email, role, token, invited_by, project_ids,
	       accepted_at, expires_at, created_at
	FROM org_invitations
	WHERE id = :id`

	var db invitationDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return invitationbus.Invitation{}, fmt.Errorf("db: %w", invitationbus.ErrNotFound)
		}
		return invitationbus.Invitation{}, fmt.Errorf("db: %w", err)
	}

	return toBusInvitation(db)
}

// QueryByToken gets an invitation by its signed token string.
func (s *Store) QueryByToken(ctx context.Context, token string) (invitationbus.Invitation, error) {
	data := struct {
		Token string `db:"token"`
	}{Token: token}

	const q = `
	SELECT id, org_id, email, role, token, invited_by, project_ids,
	       accepted_at, expires_at, created_at
	FROM org_invitations
	WHERE token = :token`

	var db invitationDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return invitationbus.Invitation{}, fmt.Errorf("db: %w", invitationbus.ErrNotFound)
		}
		return invitationbus.Invitation{}, fmt.Errorf("db: %w", err)
	}

	return toBusInvitation(db)
}

// QueryByOrg returns all invitations for the given org ordered by created_at desc.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]invitationbus.Invitation, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT id, org_id, email, role, token, invited_by, project_ids,
	       accepted_at, expires_at, created_at
	FROM org_invitations
	WHERE org_id = :org_id
	ORDER BY created_at DESC`

	var dbs []invitationDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusInvitations(dbs)
}

// Delete removes an invitation row.
func (s *Store) Delete(ctx context.Context, invID uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: invID.String()}

	const q = `DELETE FROM org_invitations WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}
	return nil
}

// MarkAccepted stamps the accepted_at column.
func (s *Store) MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error {
	data := struct {
		ID         string    `db:"id"`
		AcceptedAt time.Time `db:"accepted_at"`
	}{
		ID:         invID.String(),
		AcceptedAt: acceptedAt.UTC(),
	}

	const q = `UPDATE org_invitations SET accepted_at = :accepted_at WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}
	return nil
}
