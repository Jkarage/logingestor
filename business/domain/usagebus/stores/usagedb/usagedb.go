// Package usagedb contains ingest-usage CRUD functionality.
package usagedb

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for ingest-usage database access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// Record upserts a daily usage delta for a source.
func (s *Store) Record(ctx context.Context, u usagebus.Usage) error {
	data := struct {
		SourceID     string    `db:"source_id"`
		OrgID        string    `db:"org_id"`
		ProjectID    string    `db:"project_id"`
		Day          time.Time `db:"day"`
		EventCount   int64     `db:"event_count"`
		ByteCount    int64     `db:"byte_count"`
		DroppedCount int64     `db:"dropped_count"`
	}{
		SourceID:     u.SourceID.String(),
		OrgID:        u.OrgID.String(),
		ProjectID:    u.ProjectID.String(),
		Day:          u.Day.UTC(),
		EventCount:   u.EventCount,
		ByteCount:    u.ByteCount,
		DroppedCount: u.DroppedCount,
	}

	const q = `
	INSERT INTO ingest_usage
		(source_id, org_id, project_id, day, event_count, byte_count, dropped_count)
	VALUES
		(:source_id, :org_id, :project_id, :day, :event_count, :byte_count, :dropped_count)
	ON CONFLICT (source_id, day) DO UPDATE SET
		event_count   = ingest_usage.event_count + EXCLUDED.event_count,
		byte_count    = ingest_usage.byte_count + EXCLUDED.byte_count,
		dropped_count = ingest_usage.dropped_count + EXCLUDED.dropped_count`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UsedToday returns the total infra events ingested by an org on the given day.
func (s *Store) UsedToday(ctx context.Context, orgID uuid.UUID, day time.Time) (int64, error) {
	data := struct {
		OrgID string    `db:"org_id"`
		Day   time.Time `db:"day"`
	}{OrgID: orgID.String(), Day: day.UTC()}

	const q = `
	SELECT COALESCE(SUM(event_count), 0) AS used
	FROM ingest_usage
	WHERE org_id = :org_id AND day = :day`

	var row struct {
		Used int64 `db:"used"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &row); err != nil {
		return 0, fmt.Errorf("namedquerystruct: %w", err)
	}

	return row.Used, nil
}

// Quota returns the org's daily infra-event quota from its active plan's
// features (-1 means unlimited). A missing subscription yields -1 (unlimited).
func (s *Store) Quota(ctx context.Context, orgID uuid.UUID) (int64, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT COALESCE((p.features->>'infra_daily_event_quota')::bigint, -1) AS quota
	FROM subscriptions s
	JOIN plans p ON p.id = s.plan_id
	WHERE s.org_id = :org_id`

	var row struct {
		Quota int64 `db:"quota"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &row); err != nil {
		// No subscription row -> treat as unlimited rather than blocking ingest.
		return -1, nil
	}

	return row.Quota, nil
}
