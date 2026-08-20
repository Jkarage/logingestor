// Package apikeydb contains API key database access.
package apikeydb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages API key data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

type keyDB struct {
	ID          uuid.UUID  `db:"id"`
	OrgID       uuid.UUID  `db:"org_id"`
	ProjectID   *uuid.UUID `db:"project_id"`
	Name        string     `db:"name"`
	KeyPrefix   string     `db:"key_prefix"`
	KeyHash     string     `db:"key_hash"`
	IsActive    bool       `db:"is_active"`
	CreatedBy   *uuid.UUID `db:"created_by"`
	LastUsedAt  *time.Time `db:"last_used_at"`
	ExpiresAt   *time.Time `db:"expires_at"`
	DateCreated time.Time  `db:"date_created"`
}

func toDB(k apikeybus.APIKey) keyDB {
	return keyDB{
		ID: k.ID, OrgID: k.OrgID, ProjectID: k.ProjectID,
		Name: k.Name, KeyPrefix: k.KeyPrefix, KeyHash: k.KeyHash,
		IsActive: k.IsActive, CreatedBy: k.CreatedBy,
		LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt,
		DateCreated: k.DateCreated.UTC(),
	}
}

func toBus(db keyDB) apikeybus.APIKey {
	return apikeybus.APIKey{
		ID: db.ID, OrgID: db.OrgID, ProjectID: db.ProjectID,
		Name: db.Name, KeyPrefix: db.KeyPrefix, KeyHash: db.KeyHash,
		IsActive: db.IsActive, CreatedBy: db.CreatedBy,
		LastUsedAt: db.LastUsedAt, ExpiresAt: db.ExpiresAt,
		DateCreated: db.DateCreated.In(time.Local),
	}
}

const columns = `id, org_id, project_id, name, key_prefix, key_hash, is_active,
	created_by, last_used_at, expires_at, date_created`

// Create inserts an API key.
func (s *Store) Create(ctx context.Context, k apikeybus.APIKey) error {
	const q = `
	INSERT INTO api_keys
		(id, org_id, project_id, name, key_prefix, key_hash, is_active, created_by,
		 last_used_at, expires_at, date_created)
	VALUES
		(:id, :org_id, :project_id, :name, :key_prefix, :key_hash, :is_active, :created_by,
		 :last_used_at, :expires_at, :date_created)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDB(k)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes an API key.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `DELETE FROM api_keys WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryByID returns one key.
func (s *Store) QueryByID(ctx context.Context, id uuid.UUID) (apikeybus.APIKey, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `SELECT ` + columns + ` FROM api_keys WHERE id = :id`

	var db keyDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return apikeybus.APIKey{}, fmt.Errorf("db: %w", apikeybus.ErrNotFound)
		}
		return apikeybus.APIKey{}, fmt.Errorf("db: %w", err)
	}

	return toBus(db), nil
}

// QueryByOrg lists an org's keys, newest first.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]apikeybus.APIKey, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + columns + ` FROM api_keys WHERE org_id = :org_id ORDER BY date_created DESC`

	var rows []keyDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]apikeybus.APIKey, len(rows))
	for i, r := range rows {
		out[i] = toBus(r)
	}

	return out, nil
}

// QueryByKeyHash resolves a presented key. A miss is the normal case for a bad
// key, so it is not logged as a database error.
func (s *Store) QueryByKeyHash(ctx context.Context, keyHash string) (apikeybus.APIKey, error) {
	data := struct {
		KeyHash string `db:"key_hash"`
	}{KeyHash: keyHash}

	const q = `SELECT ` + columns + ` FROM api_keys WHERE key_hash = :key_hash`

	var db keyDB
	if err := sqldb.NamedQueryStructAllowNotFound(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return apikeybus.APIKey{}, fmt.Errorf("db: %w", apikeybus.ErrNotFound)
		}
		return apikeybus.APIKey{}, fmt.Errorf("db: %w", err)
	}

	return toBus(db), nil
}

// TouchLastUsed records that a key was used.
func (s *Store) TouchLastUsed(ctx context.Context, id uuid.UUID, when time.Time) error {
	data := struct {
		ID   string    `db:"id"`
		When time.Time `db:"when"`
	}{ID: id.String(), When: when.UTC()}

	const q = `UPDATE api_keys SET last_used_at = :when WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}
