// Package scimdb contains SCIM token database access.
package scimdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages SCIM token data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

type tokenDB struct {
	OrgID       uuid.UUID    `db:"org_id"`
	TokenHash   string       `db:"token_hash"`
	TokenPrefix string       `db:"token_prefix"`
	DateCreated time.Time    `db:"date_created"`
	LastUsedAt  sql.NullTime `db:"last_used_at"`
}

func toBusToken(db tokenDB) scimbus.Token {
	t := scimbus.Token{
		OrgID:       db.OrgID,
		TokenHash:   db.TokenHash,
		TokenPrefix: db.TokenPrefix,
		DateCreated: db.DateCreated.In(time.Local),
	}
	if db.LastUsedAt.Valid {
		v := db.LastUsedAt.Time.In(time.Local)
		t.LastUsedAt = &v
	}
	return t
}

// Upsert writes an org's token, replacing any existing one.
func (s *Store) Upsert(ctx context.Context, t scimbus.Token) error {
	data := tokenDB{
		OrgID:       t.OrgID,
		TokenHash:   t.TokenHash,
		TokenPrefix: t.TokenPrefix,
		DateCreated: t.DateCreated.UTC(),
	}

	const q = `
	INSERT INTO org_scim_tokens
		(org_id, token_hash, token_prefix, date_created, last_used_at)
	VALUES
		(:org_id, :token_hash, :token_prefix, :date_created, :last_used_at)
	ON CONFLICT (org_id) DO UPDATE SET
		token_hash = EXCLUDED.token_hash,
		token_prefix = EXCLUDED.token_prefix,
		date_created = EXCLUDED.date_created,
		last_used_at = NULL`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

const tokenColumns = `org_id, token_hash, token_prefix, date_created, last_used_at`

// QueryByHash looks a token up by its stored hash.
func (s *Store) QueryByHash(ctx context.Context, hash string) (scimbus.Token, error) {
	data := struct {
		Hash string `db:"token_hash"`
	}{Hash: hash}

	const q = `SELECT ` + tokenColumns + ` FROM org_scim_tokens WHERE token_hash = :token_hash`

	var db tokenDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return scimbus.Token{}, fmt.Errorf("db: %w", scimbus.ErrNotFound)
		}
		return scimbus.Token{}, fmt.Errorf("db: %w", err)
	}

	return toBusToken(db), nil
}

// QueryByOrg returns an org's token metadata.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) (scimbus.Token, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + tokenColumns + ` FROM org_scim_tokens WHERE org_id = :org_id`

	var db tokenDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return scimbus.Token{}, fmt.Errorf("db: %w", scimbus.ErrNotFound)
		}
		return scimbus.Token{}, fmt.Errorf("db: %w", err)
	}

	return toBusToken(db), nil
}

// Delete removes an org's token.
func (s *Store) Delete(ctx context.Context, orgID uuid.UUID) error {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, `DELETE FROM org_scim_tokens WHERE org_id = :org_id`, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// TouchLastUsed records that the token was presented.
func (s *Store) TouchLastUsed(ctx context.Context, orgID uuid.UUID, when time.Time) error {
	data := struct {
		OrgID      string    `db:"org_id"`
		LastUsedAt time.Time `db:"last_used_at"`
	}{OrgID: orgID.String(), LastUsedAt: when.UTC()}

	const q = `UPDATE org_scim_tokens SET last_used_at = :last_used_at WHERE org_id = :org_id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}
