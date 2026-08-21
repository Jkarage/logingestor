// Package usagedb contains ingest-usage CRUD functionality.
package usagedb

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
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

// Record folds a batch's counters into the source's daily tally and into its
// hourly counters. Both are written in one transaction: the daily row is what
// quota is enforced from and the hourly rows are what health is read from, and a
// source that looks quiet on one and busy on the other is worse than either
// being briefly stale.
func (s *Store) Record(ctx context.Context, u usagebus.Usage) error {
	data := struct {
		SourceID     string    `db:"source_id"`
		OrgID        string    `db:"org_id"`
		ProjectID    string    `db:"project_id"`
		Day          time.Time `db:"day"`
		Hour         time.Time `db:"hour"`
		EventCount   int64     `db:"event_count"`
		ByteCount    int64     `db:"byte_count"`
		DroppedCount int64     `db:"dropped_count"`
		ErrorCount   int64     `db:"error_count"`
		RejectCount  int64     `db:"reject_count"`
	}{
		SourceID:  u.SourceID.String(),
		OrgID:     u.OrgID.String(),
		ProjectID: u.ProjectID.String(),
		Day:       u.Day.UTC(),

		// Truncating in UTC matters: in a +05:45 zone a local truncation lands
		// mid-hour and the buckets stop lining up with the grid the API returns.
		Hour:         u.Day.UTC().Truncate(time.Hour),
		EventCount:   u.EventCount,
		ByteCount:    u.ByteCount,
		DroppedCount: u.DroppedCount,
		ErrorCount:   u.ErrorCount,
		RejectCount:  u.RejectCount,
	}

	const dailyQ = `
	INSERT INTO ingest_usage
		(source_id, org_id, project_id, day, event_count, byte_count, dropped_count)
	VALUES
		(:source_id, :org_id, :project_id, :day, :event_count, :byte_count, :dropped_count)
	ON CONFLICT (source_id, day) DO UPDATE SET
		event_count   = ingest_usage.event_count + EXCLUDED.event_count,
		byte_count    = ingest_usage.byte_count + EXCLUDED.byte_count,
		dropped_count = ingest_usage.dropped_count + EXCLUDED.dropped_count`

	const hourlyQ = `
	INSERT INTO ingest_stats_hourly
		(source_id, hour, event_count, error_count, dropped_count, reject_count)
	VALUES
		(:source_id, :hour, :event_count, :error_count, :dropped_count, :reject_count)
	ON CONFLICT (source_id, hour) DO UPDATE SET
		event_count   = ingest_stats_hourly.event_count + EXCLUDED.event_count,
		error_count   = ingest_stats_hourly.error_count + EXCLUDED.error_count,
		dropped_count = ingest_stats_hourly.dropped_count + EXCLUDED.dropped_count,
		reject_count  = ingest_stats_hourly.reject_count + EXCLUDED.reject_count`

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begintxx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{dailyQ, hourlyQ} {
		if err := sqldb.NamedExecContext(ctx, s.log, tx, q, data); err != nil {
			return fmt.Errorf("namedexeccontext: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// QuerySourceCounters totals the hourly counters per source since from.
//
// The hour column is compared against a truncated bound so a request part-way
// through an hour still includes that whole hour's row; a health figure that
// dropped the current hour would read as a stall right after ingest resumed.
func (s *Store) QuerySourceCounters(ctx context.Context, sourceIDs []uuid.UUID, from time.Time) (map[uuid.UUID]usagebus.SourceCounters, error) {
	ids := make(dbarray.String, len(sourceIDs))
	for i, id := range sourceIDs {
		ids[i] = id.String()
	}

	data := struct {
		SourceIDs dbarray.String `db:"source_ids"`
		From      time.Time      `db:"from"`
	}{SourceIDs: ids, From: from.UTC().Truncate(time.Hour)}

	const q = `
	SELECT source_id,
		COALESCE(sum(event_count), 0)   AS events,
		COALESCE(sum(error_count), 0)   AS errors,
		COALESCE(sum(dropped_count), 0) AS dropped
	FROM ingest_stats_hourly
	WHERE source_id = ANY(CAST(:source_ids AS uuid[])) AND hour >= :from
	GROUP BY source_id`

	var rows []sourceCountersDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make(map[uuid.UUID]usagebus.SourceCounters, len(rows))
	for _, r := range rows {
		id, err := uuid.Parse(r.SourceID)
		if err != nil {
			return nil, fmt.Errorf("parse source id: %w", err)
		}
		out[id] = usagebus.SourceCounters{Events: r.Events, Errors: r.Errors, Dropped: r.Dropped}
	}

	return out, nil
}

// QuerySourceBuckets returns one source's hourly counters over [from, to),
// oldest first.
func (s *Store) QuerySourceBuckets(ctx context.Context, sourceID uuid.UUID, from, to time.Time) ([]usagebus.HourCounters, error) {
	data := struct {
		SourceID string    `db:"source_id"`
		From     time.Time `db:"from"`
		To       time.Time `db:"to"`
	}{SourceID: sourceID.String(), From: from.UTC().Truncate(time.Hour), To: to.UTC()}

	const q = `
	SELECT hour, event_count AS events, error_count AS errors, dropped_count AS dropped
	FROM ingest_stats_hourly
	WHERE source_id = :source_id AND hour >= :from AND hour < :to
	ORDER BY hour`

	var rows []hourCountersDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]usagebus.HourCounters, len(rows))
	for i, r := range rows {
		out[i] = usagebus.HourCounters{Hour: r.Hour.UTC(), Events: r.Events, Errors: r.Errors, Dropped: r.Dropped}
	}

	return out, nil
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

// QueryByOrg aggregates the daily per-source counters into per-project totals
// over [from, to). Uses ingest_usage_org_day_idx.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]usagebus.ProjectUsage, error) {
	data := struct {
		OrgID string    `db:"org_id"`
		From  time.Time `db:"from_day"`
		To    time.Time `db:"to_day"`
	}{
		OrgID: orgID.String(),
		From:  from,
		To:    to,
	}

	// Left join so a project keeps its name even if its only source has since
	// been deleted; ingest_usage cascades on source, not project.
	// Infra counters live in ingest_usage (per source) and app counters in
	// app_usage (per project); a project may have either or both, so they are
	// unioned before aggregating. app_usage has no dropped column — app-log
	// ingestion rejects a batch outright rather than shedding rows.
	const q = `
	WITH combined AS (
		SELECT project_id, event_count, byte_count, dropped_count
		FROM ingest_usage
		WHERE org_id = :org_id AND day >= :from_day AND day < :to_day
		UNION ALL
		SELECT project_id, event_count, byte_count, 0 AS dropped_count
		FROM app_usage
		WHERE org_id = :org_id AND day >= :from_day AND day < :to_day
	)
	SELECT
		c.project_id,
		COALESCE(p.name, '') AS project_name,
		COALESCE(sum(c.event_count), 0)   AS event_count,
		COALESCE(sum(c.byte_count), 0)    AS byte_count,
		COALESCE(sum(c.dropped_count), 0) AS dropped_count
	FROM combined c
	LEFT JOIN projects p ON p.id = c.project_id
	GROUP BY c.project_id, p.name
	ORDER BY event_count DESC, project_name ASC`

	var rows []struct {
		ProjectID    uuid.UUID `db:"project_id"`
		ProjectName  string    `db:"project_name"`
		EventCount   int64     `db:"event_count"`
		ByteCount    int64     `db:"byte_count"`
		DroppedCount int64     `db:"dropped_count"`
	}

	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]usagebus.ProjectUsage, len(rows))
	for i, r := range rows {
		out[i] = usagebus.ProjectUsage{
			ProjectID:    r.ProjectID,
			ProjectName:  r.ProjectName,
			EventCount:   r.EventCount,
			ByteCount:    r.ByteCount,
			DroppedCount: r.DroppedCount,
		}
	}

	return out, nil
}

// RecordApp folds an app-log delta into the project's daily tally.
func (s *Store) RecordApp(ctx context.Context, u usagebus.AppUsage) error {
	data := struct {
		ProjectID  string    `db:"project_id"`
		OrgID      string    `db:"org_id"`
		Day        time.Time `db:"day"`
		EventCount int64     `db:"event_count"`
		ByteCount  int64     `db:"byte_count"`
	}{
		ProjectID:  u.ProjectID.String(),
		OrgID:      u.OrgID.String(),
		Day:        u.Day.UTC(),
		EventCount: u.EventCount,
		ByteCount:  u.ByteCount,
	}

	const q = `
	INSERT INTO app_usage
		(project_id, org_id, day, event_count, byte_count)
	VALUES
		(:project_id, :org_id, :day, :event_count, :byte_count)
	ON CONFLICT (project_id, day) DO UPDATE SET
		event_count = app_usage.event_count + EXCLUDED.event_count,
		byte_count  = app_usage.byte_count + EXCLUDED.byte_count`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// AppUsedToday returns the app-log events an org ingested on the given day.
func (s *Store) AppUsedToday(ctx context.Context, orgID uuid.UUID, day time.Time) (int64, error) {
	data := struct {
		OrgID string    `db:"org_id"`
		Day   time.Time `db:"day"`
	}{OrgID: orgID.String(), Day: day.UTC()}

	const q = `
	SELECT COALESCE(sum(event_count), 0) AS used
	FROM app_usage
	WHERE org_id = :org_id AND day = :day`

	var row struct {
		Used int64 `db:"used"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &row); err != nil {
		return 0, fmt.Errorf("namedquerystruct: %w", err)
	}

	return row.Used, nil
}

// AppQuota returns the org plan's daily app-log event quota; -1 is unlimited.
func (s *Store) AppQuota(ctx context.Context, orgID uuid.UUID) (int64, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT COALESCE((p.features->>'app_daily_event_quota')::bigint, -1) AS quota
	FROM subscriptions s
	JOIN plans p ON p.id = s.plan_id
	WHERE s.org_id = :org_id`

	var row struct {
		Quota int64 `db:"quota"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &row); err != nil {
		// No subscription row -> unlimited rather than blocking ingest.
		return -1, nil
	}

	return row.Quota, nil
}
