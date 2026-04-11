// Package logdb contains log related CRUD functionality.
package logdb

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for log database access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// BulkInsert persists a slice of log entries, one INSERT per entry.
func (s *Store) BulkInsert(ctx context.Context, logs []logbus.Log) error {
	const q = `
	INSERT INTO logs
		(id, project_id, level, message, source, ts, tags, meta)
	VALUES
		(:id, :project_id, :level, :message, :source, :ts, :tags, :meta)`

	for _, l := range logs {
		if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBLog(l)); err != nil {
			return fmt.Errorf("namedexeccontext: %w", err)
		}
	}

	return nil
}

// Query returns logs matching filter, ordered ts DESC.
// afterTs/afterID implement keyset cursor pagination.
func (s *Store) Query(ctx context.Context, filter logbus.QueryFilter, limit int, afterTs *time.Time, afterID *uuid.UUID) ([]logbus.Log, int, error) {
	data := map[string]any{
		"project_id": filter.ProjectID.String(),
		"limit":      limit,
	}

	base := `
	SELECT id, project_id, level, message, source, ts, tags, meta
	FROM logs`

	countBase := `SELECT count(1) FROM logs`

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

	// Total count uses the same filters but without the cursor condition.
	var count struct {
		Count int `db:"count"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, countBuf.String(), data, &count); err != nil {
		return nil, 0, fmt.Errorf("namedquerystruct count: %w", err)
	}

	return logs, count.Count, nil
}

// Stats returns a per-level count for a project.
func (s *Store) Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error) {
	data := struct {
		ProjectID string `db:"project_id"`
	}{
		ProjectID: projectID.String(),
	}

	const q = `
	SELECT level, count(1) AS count
	FROM logs
	WHERE project_id = :project_id
	GROUP BY level`

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

	if filter.Level != nil {
		data["level"] = filter.Level.String()
		writeWhere(dataBuf, "level = :level")
		writeWhere(countBuf, "level = :level")
	}

	if filter.Search != nil {
		data["search"] = "%" + *filter.Search + "%"
		writeWhere(dataBuf, "(message ILIKE :search OR source ILIKE :search)")
		writeWhere(countBuf, "(message ILIKE :search OR source ILIKE :search)")
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
