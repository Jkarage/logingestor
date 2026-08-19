// Package logdb contains log related CRUD functionality.
package logdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// logColumns is the shared SELECT column list for logs rows; it must match the
// db tags on logDB.
const logColumns = `id, project_id, level, message, source, ts, tags, meta,
	source_type, source_id, host, container, pod, namespace, cluster,
	unit, facility, region, cloud_resource_id, attributes`

// Store manages the set of APIs for log database access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// insertBatchSize bounds the rows per multi-row INSERT so the total bound
// parameter count stays well under Postgres' 65535 limit (20 cols * 1000 rows).
const insertBatchSize = 1000

// BulkInsert persists a slice of log entries using chunked multi-row INSERTs.
// sqlx expands a slice argument into VALUES (...),(...),... for one round trip
// per chunk — far cheaper than the previous per-row loop on the infra path.
func (s *Store) BulkInsert(ctx context.Context, logs []logbus.Log) error {
	const q = `
	INSERT INTO logs
		(id, project_id, level, message, source, ts, tags, meta,
		 source_type, source_id, host, container, pod, namespace, cluster,
		 unit, facility, region, cloud_resource_id, attributes)
	VALUES
		(:id, :project_id, :level, :message, :source, :ts, :tags, :meta,
		 :source_type, :source_id, :host, :container, :pod, :namespace, :cluster,
		 :unit, :facility, :region, :cloud_resource_id, :attributes)`

	// The rows and the log_stats_daily deltas commit together so the rollup that
	// backs /logs/stats can never drift from the rows it counts.
	const rollupQ = `
	INSERT INTO log_stats_hourly
		(project_id, hour, source_type, source, level, count)
	VALUES
		(:project_id, :hour, :source_type, :source, :level, :count)
	ON CONFLICT (project_id, hour, source_type, source, level)
	DO UPDATE SET count = log_stats_hourly.count + EXCLUDED.count`

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begintxx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(logs); start += insertBatchSize {
		end := min(start+insertBatchSize, len(logs))

		rows := make([]logDB, 0, end-start)
		for _, l := range logs[start:end] {
			rows = append(rows, toDBLog(l))
		}

		if _, err := tx.NamedExecContext(ctx, q, rows); err != nil {
			return fmt.Errorf("namedexeccontext: %w", err)
		}
	}

	if deltas := rollupDeltas(logs); len(deltas) > 0 {
		if _, err := tx.NamedExecContext(ctx, rollupQ, deltas); err != nil {
			return fmt.Errorf("namedexeccontext rollup: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// rollupDeltas collapses a batch into one per-(project, UTC hour, source_type,
// source, level) increment so a batch of N rows costs a handful of upserts, not N.
func rollupDeltas(logs []logbus.Log) []statsDeltaDB {
	type key struct {
		projectID  uuid.UUID
		hour       time.Time
		sourceType string
		source     string
		level      string
	}

	counts := make(map[key]int)
	for _, l := range logs {
		k := key{
			projectID:  l.ProjectID,
			hour:       UTCHour(l.Timestamp),
			sourceType: l.SourceType,
			source:     l.Source,
			level:      l.Level.String(),
		}
		counts[k]++
	}

	deltas := make([]statsDeltaDB, 0, len(counts))
	for k, n := range counts {
		deltas = append(deltas, statsDeltaDB{
			ProjectID:  k.projectID,
			Hour:       k.hour,
			SourceType: k.sourceType,
			Source:     k.source,
			Level:      k.level,
			Count:      n,
		})
	}

	return deltas
}

// UTCHour truncates t to the start of its UTC hour, matching the bucket the
// rollup is keyed on. Truncating in any other zone would land mid-hour for
// offsets like +05:45 and split a single hour across two buckets.
func UTCHour(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), utc.Hour(), 0, 0, 0, time.UTC)
}

// QueryByID returns the log with the given id.
func (s *Store) QueryByID(ctx context.Context, id uuid.UUID) (logbus.Log, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `
	SELECT ` + logColumns + `
	FROM logs
	WHERE id = :id`

	var db logDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return logbus.Log{}, fmt.Errorf("db: %w", logbus.ErrNotFound)
		}
		return logbus.Log{}, fmt.Errorf("db: %w", err)
	}

	return toBusLog(db)
}

// Query returns logs matching filter, ordered ts DESC.
// afterTs/afterID implement keyset cursor pagination.
func (s *Store) Query(ctx context.Context, filter logbus.QueryFilter, limit int, afterTs *time.Time, afterID *uuid.UUID) ([]logbus.Log, int, error) {
	data := map[string]any{
		"project_id": filter.ProjectID.String(),
		"limit":      limit,
	}

	base := `
	SELECT ` + logColumns + `
	FROM logs`

	// The count is built as an inner SELECT so a bounded count can wrap it in a
	// LIMIT. An exact count over a project holding most of the table degenerates
	// to a sequential scan (~10s at 50M rows), so it is opt-in only.
	countBase := `SELECT 1 FROM logs`

	buf := bytes.NewBufferString(base)
	countBuf := bytes.NewBufferString(countBase)

	applyWhere(filter, afterTs, afterID, data, buf, countBuf)

	buf.WriteString(" ORDER BY ts DESC, id DESC LIMIT :limit")

	var dbLogs []logDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &dbLogs); err != nil {
		return nil, 0, fmt.Errorf("namedqueryslice: %w", err)
	}

	logs, err := toBusLogs(dbLogs)
	if err != nil {
		return nil, 0, err
	}

	if filter.TotalMode == logbus.TotalNone {
		return logs, 0, nil
	}

	// The total uses the same filters but never the cursor condition, so a page
	// deep into the results still reports the whole matching set.
	var countQuery string
	switch filter.TotalMode {
	case logbus.TotalExact:
		countQuery = "SELECT count(1) AS count FROM (" + countBuf.String() + ") t"
	default:
		// Stop counting at the cap. The ORDER BY keeps this on
		// logs_project_ts_idx instead of falling back to a seq scan.
		data["count_cap"] = logbus.TotalCap
		countQuery = "SELECT count(1) AS count FROM (" + countBuf.String() +
			" ORDER BY ts DESC LIMIT :count_cap) t"
	}

	var count struct {
		Count int `db:"count"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, countQuery, data, &count); err != nil {
		return nil, 0, fmt.Errorf("namedquerystruct count: %w", err)
	}

	return logs, count.Count, nil
}

// Stats returns a per-level count for a project, optionally scoped to a
// source_type ("app" | "infra"); nil counts all source types.
func (s *Store) Stats(ctx context.Context, projectID uuid.UUID, sourceType *string) (map[string]int, error) {
	data := map[string]any{
		"project_id": projectID.String(),
	}

	// Served from the log_stats_hourly rollup (migration 1.27). Counting from
	// logs directly cost ~11s on a 50M-row project because a per-level
	// GROUP BY has to visit every row for the project.
	q := `
	SELECT level, COALESCE(sum(count), 0) AS count
	FROM log_stats_hourly
	WHERE project_id = :project_id`

	if sourceType != nil {
		data["source_type"] = *sourceType
		q += ` AND source_type = :source_type`
	}

	q += ` GROUP BY level`

	var rows []statsRow
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	counts := map[string]int{
		"DEBUG": 0,
		"INFO":  0,
		"WARN":  0,
		"ERROR": 0,
	}
	for _, r := range rows {
		counts[r.Level] = r.Count
	}

	return counts, nil
}

// applyWhere adds WHERE clauses to both the data query buffer and the count
// query buffer. The cursor condition is applied to the data buffer only.
func applyWhere(filter logbus.QueryFilter, afterTs *time.Time, afterID *uuid.UUID, data map[string]any, dataBuf, countBuf *bytes.Buffer) {
	writeWhere := func(buf *bytes.Buffer, clause string) {
		if bytes.Contains(buf.Bytes(), []byte("WHERE")) {
			buf.WriteString(" AND " + clause)
		} else {
			buf.WriteString(" WHERE " + clause)
		}
	}

	writeWhere(dataBuf, "project_id = :project_id")
	writeWhere(countBuf, "project_id = :project_id")

	if filter.SourceType != nil {
		data["source_type"] = *filter.SourceType
		writeWhere(dataBuf, "source_type = :source_type")
		writeWhere(countBuf, "source_type = :source_type")
	}

	if len(filter.Levels) > 0 {
		levels := make(dbarray.String, len(filter.Levels))
		for i, l := range filter.Levels {
			levels[i] = l.String()
		}
		data["levels"] = levels
		writeWhere(dataBuf, "level = ANY(:levels)")
		writeWhere(countBuf, "level = ANY(:levels)")
	}

	if filter.Search != nil {
		// The pattern is bound as a parameter, so wildcards inside the caller's
		// text are matched literally by LIKE semantics only — never as SQL.
		data["search"] = "%" + *filter.Search + "%"
		writeWhere(dataBuf, "(message ILIKE :search OR source ILIKE :search)")
		writeWhere(countBuf, "(message ILIKE :search OR source ILIKE :search)")
	}

	if filter.Source != nil {
		data["source"] = *filter.Source
		writeWhere(dataBuf, "source = :source")
		writeWhere(countBuf, "source = :source")
	}

	if len(filter.Tags) > 0 {
		tags := make(dbarray.String, len(filter.Tags))
		copy(tags, filter.Tags)
		data["tags"] = tags
		// @> requires every listed tag, so repeating ?tag= narrows the result.
		writeWhere(dataBuf, "tags @> :tags")
		writeWhere(countBuf, "tags @> :tags")
	}

	if filter.From != nil {
		data["from_ts"] = filter.From.UTC()
		writeWhere(dataBuf, "ts >= :from_ts")
		writeWhere(countBuf, "ts >= :from_ts")
	}

	if filter.To != nil {
		data["to_ts"] = filter.To.UTC()
		writeWhere(dataBuf, "ts <= :to_ts")
		writeWhere(countBuf, "ts <= :to_ts")
	}

	// Cursor applies to data query only (not count).
	if afterTs != nil && afterID != nil {
		data["cursor_ts"] = afterTs.UTC()
		data["cursor_id"] = afterID.String()
		writeWhere(dataBuf, "(ts < :cursor_ts OR (ts = :cursor_ts AND CAST(id AS text) < :cursor_id))")
	}
}
