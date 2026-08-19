// Package sourcedb contains source related CRUD functionality.
package sourcedb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// sourceColumns is the shared SELECT column list; must match sourceDB db tags.
const sourceColumns = `id, org_id, project_id, kind, name, key_prefix, key_hash,
	is_active, last_seen_at, rate_limit_per_sec, rate_limit_burst,
	sample_debug_info, created_at, expires_at`

// Store manages the set of APIs for source database access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// Create inserts a new source into the database.
func (s *Store) Create(ctx context.Context, source sourcebus.Source) error {
	const q = `
	INSERT INTO sources
		(id, org_id, project_id, kind, name, key_prefix, key_hash, is_active,
		 last_seen_at, rate_limit_per_sec, rate_limit_burst, sample_debug_info, created_at,
		 expires_at)
	VALUES
		(:id, :org_id, :project_id, :kind, :name, :key_prefix, :key_hash, :is_active,
		 :last_seen_at, :rate_limit_per_sec, :rate_limit_burst, :sample_debug_info, :created_at,
		 :expires_at)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBSource(source)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", sourcebus.ErrDuplicateName)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update replaces a source's mutable fields (is_active, key on rotation).
func (s *Store) Update(ctx context.Context, source sourcebus.Source) error {
	const q = `
	UPDATE sources
	SET
		name              = :name,
		is_active         = :is_active,
		key_prefix        = :key_prefix,
		key_hash          = :key_hash,
		rate_limit_per_sec = :rate_limit_per_sec,
		rate_limit_burst  = :rate_limit_burst,
		sample_debug_info = :sample_debug_info
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBSource(source)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryByOrg returns all sources for an org, newest first.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]sourcebus.Source, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	q := `
	SELECT ` + sourceColumns + `
	FROM sources
	WHERE org_id = :org_id
	ORDER BY created_at DESC`

	var rows []sourceDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusSources(rows), nil
}

// QueryByID returns the source with the given id.
func (s *Store) QueryByID(ctx context.Context, id uuid.UUID) (sourcebus.Source, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	q := `
	SELECT ` + sourceColumns + `
	FROM sources
	WHERE id = :id`

	var db sourceDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return sourcebus.Source{}, fmt.Errorf("db: %w", sourcebus.ErrNotFound)
		}
		return sourcebus.Source{}, fmt.Errorf("db: %w", err)
	}

	return toBusSource(db), nil
}

// QueryByKeyHash resolves a source by its key hash (ingestion auth path).
func (s *Store) QueryByKeyHash(ctx context.Context, keyHash string) (sourcebus.Source, error) {
	data := struct {
		KeyHash string `db:"key_hash"`
	}{KeyHash: keyHash}

	q := `
	SELECT ` + sourceColumns + `
	FROM sources
	WHERE key_hash = :key_hash`

	var db sourceDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return sourcebus.Source{}, fmt.Errorf("db: %w", sourcebus.ErrNotFound)
		}
		return sourcebus.Source{}, fmt.Errorf("db: %w", err)
	}

	return toBusSource(db), nil
}

// TouchLastSeen updates last_seen_at for a source.
func (s *Store) TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error {
	data := struct {
		ID         string    `db:"id"`
		LastSeenAt time.Time `db:"last_seen_at"`
	}{ID: id.String(), LastSeenAt: when.UTC()}

	const q = `UPDATE sources SET last_seen_at = :last_seen_at WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}
