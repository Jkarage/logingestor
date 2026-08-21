// Package retention deletes aged log rows. It is source_type-aware: infra logs
// expire on the org plan's infra_retention_days, while app logs use the
// project's own retention_days when set and otherwise fall back to the plan's
// log_retention_days — so a project that never set an override still ages out.
//
// Deletes are issued in bounded batches against logs_project_sourcetype_ts_idx
// and are budgeted per run. A single unbatched DELETE is not viable here: the
// first pass over a backlogged project can target tens of millions of rows,
// which in one transaction means a multi-GB WAL burst, a long-held snapshot and
// heavy table bloat.
package retention

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Source types held in logs.source_type.
const (
	sourceTypeApp   = "app"
	sourceTypeInfra = "infra"
)

// keepForever is the retention_days sentinel meaning "never expire".
const keepForever = -1

// defaultSourceStatsDays is how long per-source hourly counters are kept. Source
// health reads a 24 hour window; two weeks leaves room to widen it without a
// migration and still keeps the table trivially small.
const defaultSourceStatsDays = 14

// Client error retention. Raw reports are diagnostic and lose value quickly;
// the issue they group into is the durable record and outlives them, which is
// why the two are separate numbers.
const (
	defaultClientErrorEventDays = 30
	defaultClientErrorIssueDays = 180
)

// mapGracePeriod is how long a source map survives with no events referring to
// its release. CI uploads maps as part of the deploy, which can be minutes
// before the first user loads the new bundle, so a map is never deleted for
// being briefly unreferenced.
const mapGracePeriod = 7 * 24 * time.Hour

// Config bounds one retention run so a scheduled pass makes steady progress
// without monopolising the database.
type Config struct {
	// AuditDays is how long audit records are kept. Negative keeps them forever.
	// Audit is a compliance surface, so the default is deliberately long and
	// deleting it is opt-in via configuration.
	AuditDays int

	// BatchSize is the number of rows deleted per statement.
	BatchSize int

	// MaxRows caps how many rows a single run may delete across all projects.
	// Zero means unlimited.
	MaxRows int

	// MaxRuntime caps the wall-clock time of a run. Zero means unlimited.
	MaxRuntime time.Duration

	// SourceStatsDays is how long per-source hourly ingest counters are kept.
	// They back source health, which only ever reads the last 24 hours, so this
	// is short by design and negative keeps them forever. Zero means the default.
	SourceStatsDays int

	// ClientErrorEventDays and ClientErrorIssueDays bound client error
	// monitoring. Raw browser reports are the bulk and age out first; the issues
	// they group into are small, are what a human triages, and keep their
	// first-seen history, so they live longer. Negative keeps either forever.
	ClientErrorEventDays int
	ClientErrorIssueDays int
}

// DefaultConfig is a conservative pass: ~2M rows or 10 minutes, whichever comes
// first. A backlog larger than that drains over successive runs.
func DefaultConfig() Config {
	return Config{BatchSize: 10_000, MaxRows: 2_000_000, MaxRuntime: 10 * time.Minute, AuditDays: keepForever,
		SourceStatsDays: defaultSourceStatsDays}
}

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 10_000
	}
	if c.SourceStatsDays == 0 {
		c.SourceStatsDays = defaultSourceStatsDays
	}
	if c.ClientErrorEventDays == 0 {
		c.ClientErrorEventDays = defaultClientErrorEventDays
	}
	if c.ClientErrorIssueDays == 0 {
		c.ClientErrorIssueDays = defaultClientErrorIssueDays
	}
	return c
}

// Result reports what a run deleted.
type Result struct {
	InfraDeleted int64
	AppDeleted   int64
	AuditDeleted int64

	// SourceStatsDeleted counts pruned per-source hourly counter rows. It is not
	// part of Total: those rows are derived counters, not ingested data, and
	// pruning them must not consume the run's row budget.
	SourceStatsDeleted int64

	// ClientErrorEventsDeleted and ClientErrorIssuesDeleted count pruned browser
	// error records. Also outside Total: they are our own diagnostics, not
	// customer data, and must not consume the budget that drains logs.
	ClientErrorEventsDeleted int64
	ClientErrorIssuesDeleted int64

	// Incomplete is true when the run stopped on its row or time budget, meaning
	// expired rows remain and the next run should continue.
	Incomplete bool
}

// Total returns the combined row count deleted.
func (r Result) Total() int64 { return r.InfraDeleted + r.AppDeleted + r.AuditDeleted }

// expires reports whether a resolved retention_days value causes deletion.
// The keepForever sentinel (-1, and any lower value) never expires; zero does
// expire, meaning "retain nothing".
func expires(days int) bool {
	return days > keepForever
}

// boundaryHour returns the UTC hour window containing cutoff. Retention deletes
// at an exact instant, so the hour the cutoff falls inside is only partly
// removed and its rollup rows have to be recomputed from the survivors. The
// window is pinned to UTC to match the bucketing used when the rollup is
// written; truncating in a +05:45 zone would land mid-hour.
func boundaryHour(cutoff time.Time) (start, end time.Time) {
	utc := cutoff.UTC()
	start = time.Date(utc.Year(), utc.Month(), utc.Day(), utc.Hour(), 0, 0, 0, time.UTC)
	return start, start.Add(time.Hour)
}

// target is one project's resolved retention, in days, per source type.
type target struct {
	ProjectID uuid.UUID `db:"id"`
	AppDays   int       `db:"app_days"`
	InfraDays int       `db:"infra_days"`
}

// Run deletes expired rows and keeps the log_stats_daily rollup in step.
func Run(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg Config) (Result, error) {
	cfg = cfg.withDefaults()

	var res Result

	if err := sqldb.StatusCheck(ctx, db); err != nil {
		return res, fmt.Errorf("status check: %w", err)
	}

	// Resolve every project's effective retention up front. projects, plans and
	// subscriptions are small, so this is one cheap query rather than a join
	// re-evaluated per deleted row.
	const targetsQ = `
	SELECT
		p.id,
		COALESCE(p.retention_days, (pl.features->>'log_retention_days')::int, -1)  AS app_days,
		COALESCE((pl.features->>'infra_retention_days')::int, -1)                  AS infra_days
	FROM projects p
	LEFT JOIN subscriptions s ON s.org_id = p.org_id
	LEFT JOIN plans pl        ON pl.id = s.plan_id`

	var targets []target
	if err := db.SelectContext(ctx, &targets, targetsQ); err != nil {
		return res, fmt.Errorf("resolve targets: %w", err)
	}

	start := time.Now()
	deadline := time.Time{}
	if cfg.MaxRuntime > 0 {
		deadline = start.Add(cfg.MaxRuntime)
	}

	for _, t := range targets {
		for _, pass := range []struct {
			sourceType string
			days       int
			deleted    *int64
		}{
			{sourceTypeApp, t.AppDays, &res.AppDeleted},
			{sourceTypeInfra, t.InfraDays, &res.InfraDeleted},
		} {
			if !expires(pass.days) {
				continue
			}

			n, incomplete, err := purge(ctx, log, db, cfg, t.ProjectID, pass.sourceType, pass.days, res.Total(), deadline)
			*pass.deleted += n
			if err != nil {
				return res, err
			}
			if incomplete {
				res.Incomplete = true
				log.Info(ctx, "retention budget reached", "deleted", res.Total(), "elapsed", time.Since(start).String())
				return res, nil
			}
		}
	}

	n, err := purgeAudit(ctx, log, db, cfg, deadline)
	res.AuditDeleted = n
	if err != nil {
		return res, err
	}

	res.SourceStatsDeleted, err = pruneSourceStats(ctx, log, db, cfg)
	if err != nil {
		return res, err
	}

	res.ClientErrorEventsDeleted, res.ClientErrorIssuesDeleted, err = pruneClientErrors(ctx, log, db, cfg)
	if err != nil {
		return res, err
	}

	log.Info(ctx, "retention complete",
		"infra_deleted", res.InfraDeleted,
		"app_deleted", res.AppDeleted,
		"audit_deleted", res.AuditDeleted,
		"source_stats_deleted", res.SourceStatsDeleted,
		"client_error_events_deleted", res.ClientErrorEventsDeleted,
		"elapsed", time.Since(start).String())

	return res, nil
}

// pruneSourceStats drops per-source hourly counters that have aged out.
//
// Unlike the log passes this needs no rollup repair and no batching: the table
// holds at most one row per source per hour, so even a year of a busy tenant is
// thousands of rows, and the delete is served by the index on hour.
func pruneSourceStats(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg Config) (int64, error) {
	if !expires(cfg.SourceStatsDays) {
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-time.Duration(cfg.SourceStatsDays) * 24 * time.Hour)

	const q = `DELETE FROM ingest_stats_hourly WHERE hour < $1`

	r, err := db.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune source stats: %w", err)
	}

	deleted, err := r.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	if deleted > 0 {
		log.Info(ctx, "retention pruned source stats", "days", cfg.SourceStatsDays, "deleted", deleted)
	}

	return deleted, nil
}

// pruneClientErrors ages out browser error reports and the issues they grouped
// into.
//
// Events go by when we received them, not by when the browser says they
// happened, so a device with a wrong clock cannot keep a row alive or delete it
// early. Issues go by last seen: an issue nobody has hit for six months is
// history, and deleting it takes its facets with it by cascade.
//
// The two deletes are ordered events-then-issues so an issue is never removed
// while its events still point at it.
func pruneClientErrors(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg Config) (int64, int64, error) {
	var events, issues int64

	if expires(cfg.ClientErrorEventDays) {
		cutoff := time.Now().UTC().Add(-time.Duration(cfg.ClientErrorEventDays) * 24 * time.Hour)

		const q = `DELETE FROM client_error_events WHERE received_at < $1`

		r, err := db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return 0, 0, fmt.Errorf("prune client error events: %w", err)
		}
		if n, err := r.RowsAffected(); err == nil {
			events = n
		}
	}

	if expires(cfg.ClientErrorIssueDays) {
		cutoff := time.Now().UTC().Add(-time.Duration(cfg.ClientErrorIssueDays) * 24 * time.Hour)

		// Only issues nothing points at any more: an old issue that still has a
		// recent event is still live, whatever its last_seen says.
		const q = `
		DELETE FROM client_error_issues i
		WHERE i.last_seen_at < $1
		  AND NOT EXISTS (SELECT 1 FROM client_error_events e WHERE e.issue_id = i.id)`

		r, err := db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return events, 0, fmt.Errorf("prune client error issues: %w", err)
		}
		if n, err := r.RowsAffected(); err == nil {
			issues = n
		}
	}

	// Source maps for a release nothing refers to any more are dead weight: they
	// exist only to resolve stacks, and there are no stacks left to resolve.
	// Releases with no events at all are kept for a grace period so a map
	// uploaded ahead of a deploy is not deleted before the deploy happens.
	const dropMaps = `
	DELETE FROM client_error_artifacts a
	WHERE a.date_created < $1
	  AND NOT EXISTS (SELECT 1 FROM client_error_events e WHERE e.release = a.release)`

	if r, err := db.ExecContext(ctx, dropMaps, time.Now().UTC().Add(-mapGracePeriod)); err != nil {
		return events, issues, fmt.Errorf("prune source maps: %w", err)
	} else if n, err := r.RowsAffected(); err == nil && n > 0 {
		log.Info(ctx, "retention pruned source maps", "artifacts", n)
	}

	if events > 0 || issues > 0 {
		log.Info(ctx, "retention pruned client errors",
			"event_days", cfg.ClientErrorEventDays, "events", events,
			"issue_days", cfg.ClientErrorIssueDays, "issues", issues)
	}

	return events, issues, nil
}

// purgeAudit ages out audit records, in batches like the log passes. There is no
// rollup to repair here.
func purgeAudit(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg Config, deadline time.Time) (int64, error) {
	if !expires(cfg.AuditDays) {
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-time.Duration(cfg.AuditDays) * 24 * time.Hour)

	const q = `
	DELETE FROM audit
	WHERE id IN (
		SELECT id FROM audit WHERE timestamp < $1 ORDER BY timestamp LIMIT $2
	)`

	var deleted int64

	for {
		if ctx.Err() != nil {
			return deleted, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return deleted, nil
		}

		r, err := db.ExecContext(ctx, q, cutoff, cfg.BatchSize)
		if err != nil {
			return deleted, fmt.Errorf("delete audit: %w", err)
		}

		n, err := r.RowsAffected()
		if err != nil {
			return deleted, fmt.Errorf("rows affected: %w", err)
		}

		deleted += n

		if n < int64(cfg.BatchSize) {
			break
		}
	}

	if deleted > 0 {
		log.Info(ctx, "retention purged audit", "days", cfg.AuditDays, "deleted", deleted)
	}

	return deleted, nil
}

// purge deletes one project's expired rows of a single source type and realigns
// the rollup. spent is the row count already used by this run.
//
// The repair runs on every path that deleted anything, including a budget or
// deadline stop. An earlier version returned early from the batch loop without
// repairing, which left log_stats_hourly counting rows that no longer existed —
// so /logs/stats over-reported by the size of the drain.
func purge(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg Config, projectID uuid.UUID, sourceType string, days int, spent int64, deadline time.Time) (int64, bool, error) {
	// Cutoff is computed here rather than in SQL so the same instant drives both
	// the delete and the rollup repair.
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)

	deleted, incomplete, err := deleteExpired(ctx, db, cfg, projectID, sourceType, cutoff, spent, deadline)

	// Repair before surfacing any error: rows are already gone, and leaving the
	// rollup ahead of them is worse than the error itself.
	if deleted > 0 {
		if rerr := repairRollup(ctx, db, projectID, sourceType, cutoff); rerr != nil {
			if err == nil {
				err = rerr
			} else {
				log.Error(ctx, "retention: rollup repair failed after a delete error",
					"projectID", projectID, "source_type", sourceType, "msg", rerr)
			}
		}

		log.Info(ctx, "retention purged", "projectID", projectID, "source_type", sourceType,
			"days", days, "deleted", deleted, "incomplete", incomplete)
	}

	return deleted, incomplete, err
}

// deleteExpired removes rows older than cutoff in bounded batches. It reports
// incomplete when it stopped on the run's budget rather than running out of
// rows, and never touches the rollup — purge owns that.
func deleteExpired(ctx context.Context, db *sqlx.DB, cfg Config, projectID uuid.UUID, sourceType string, cutoff time.Time, spent int64, deadline time.Time) (int64, bool, error) {
	const deleteQ = `
	DELETE FROM logs
	WHERE id IN (
		SELECT id FROM logs
		WHERE project_id = $1 AND source_type = $2 AND ts < $3
		ORDER BY ts
		LIMIT $4
	)`

	var deleted int64

	for {
		if ctx.Err() != nil {
			return deleted, true, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return deleted, true, nil
		}

		batch, ok := nextBatch(cfg, spent, deleted)
		if !ok {
			return deleted, true, nil
		}

		r, err := db.ExecContext(ctx, deleteQ, projectID, sourceType, cutoff, batch)
		if err != nil {
			return deleted, false, fmt.Errorf("delete %s logs projectID[%s]: %w", sourceType, projectID, err)
		}

		n, err := r.RowsAffected()
		if err != nil {
			return deleted, false, fmt.Errorf("rows affected: %w", err)
		}

		deleted += n

		// A short batch means this project/source type is drained.
		if n < int64(batch) {
			return deleted, false, nil
		}
	}
}

// nextBatch returns the batch size for the next delete, trimmed so the run does
// not exceed its row budget. ok is false when the budget is spent, which is the
// signal to stop — and historically the point where the rollup repair was
// skipped, so the caller must still repair whatever it already deleted.
func nextBatch(cfg Config, spent, deleted int64) (batch int, ok bool) {
	batch = cfg.BatchSize

	if cfg.MaxRows <= 0 {
		return batch, true
	}

	remaining := int64(cfg.MaxRows) - (spent + deleted)
	if remaining <= 0 {
		return 0, false
	}
	if remaining < int64(batch) {
		batch = int(remaining)
	}

	return batch, true
}

// repairRollup realigns log_stats_hourly with the rows that actually survived a
// purge.
//
// The watermark is the oldest surviving row, deliberately not the cutoff. Deletes
// run strictly oldest-first and a run stops on its budget, so a partial drain
// leaves rows behind in hours older than the cutoff. Keying the repair off the
// cutoff dropped those hours' rollup entries while their rows were still present,
// under-reporting by everything still waiting to drain — the mirror image of the
// earlier bug where the repair was skipped altogether.
//
// Because deletion is oldest-first, the three regions are exact:
//   - hours before the watermark hour are fully drained -> drop their rollup rows
//   - the watermark hour is partially drained           -> recompute it
//   - hours after it were never touched                 -> leave them alone
func repairRollup(ctx context.Context, db *sqlx.DB, projectID uuid.UUID, sourceType string, cutoff time.Time) error {
	const oldestQ = `SELECT min(ts) FROM logs WHERE project_id = $1 AND source_type = $2`

	var oldest sql.NullTime
	if err := db.GetContext(ctx, &oldest, oldestQ, projectID, sourceType); err != nil {
		return fmt.Errorf("oldest surviving row: %w", err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begintxx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Nothing of this source type survives, so every rollup row for it is stale.
	if !oldest.Valid {
		const dropAll = `
		DELETE FROM log_stats_hourly
		WHERE project_id = $1 AND source_type = $2`

		if _, err := tx.ExecContext(ctx, dropAll, projectID, sourceType); err != nil {
			return fmt.Errorf("drop rollup for drained source type: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}

		return nil
	}

	boundaryStart, boundaryEnd := boundaryHour(oldest.Time)

	const dropDrainedHours = `
	DELETE FROM log_stats_hourly
	WHERE project_id = $1 AND source_type = $2 AND hour <= $3`

	if _, err := tx.ExecContext(ctx, dropDrainedHours, projectID, sourceType, boundaryStart); err != nil {
		return fmt.Errorf("drop rollup hours: %w", err)
	}

	const rebuildBoundary = `
	INSERT INTO log_stats_hourly (project_id, hour, source_type, source, level, count)
	SELECT
		project_id,
		(date_trunc('hour', ts AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC',
		source_type,
		source,
		level,
		count(*)
	FROM logs
	WHERE project_id = $1 AND source_type = $2 AND ts >= $3 AND ts < $4
	GROUP BY 1, 2, 3, 4, 5
	ON CONFLICT (project_id, hour, source_type, source, level)
	DO UPDATE SET count = EXCLUDED.count`

	if _, err := tx.ExecContext(ctx, rebuildBoundary, projectID, sourceType, boundaryStart, boundaryEnd); err != nil {
		return fmt.Errorf("rebuild boundary hour: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
