// Package rejectdb contains rejected-record database access.
package rejectdb

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages rejected-record data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

type rejectDB struct {
	ID          uuid.UUID `db:"id"`
	SourceID    uuid.UUID `db:"source_id"`
	OrgID       uuid.UUID `db:"org_id"`
	ProjectID   uuid.UUID `db:"project_id"`
	Kind        string    `db:"kind"`
	RecordIndex int       `db:"record_index"`
	Reason      string    `db:"reason"`
	Payload     string    `db:"payload"`
	ReceivedAt  time.Time `db:"received_at"`
}

func toDB(r rejectbus.Reject) rejectDB {
	return rejectDB{
		ID: r.ID, SourceID: r.SourceID, OrgID: r.OrgID, ProjectID: r.ProjectID,
		Kind: r.Kind, RecordIndex: r.RecordIndex, Reason: r.Reason,
		Payload: r.Payload, ReceivedAt: r.ReceivedAt.UTC(),
	}
}

func toBus(db rejectDB) rejectbus.Reject {
	return rejectbus.Reject{
		ID: db.ID, SourceID: db.SourceID, OrgID: db.OrgID, ProjectID: db.ProjectID,
		Kind: db.Kind, RecordIndex: db.RecordIndex, Reason: db.Reason,
		Payload: db.Payload, ReceivedAt: db.ReceivedAt.UTC(),
	}
}

const columns = `id, source_id, org_id, project_id, kind, record_index, reason,
	payload, received_at`

// Store writes a batch of refusals in one statement, because they arrive
// together and the ingest path is waiting.
func (s *Store) Store(ctx context.Context, rejects []rejectbus.Reject) (int, error) {
	if len(rejects) == 0 {
		return 0, nil
	}

	const q = `
	INSERT INTO ingest_rejects
		(id, source_id, org_id, project_id, kind, record_index, reason, payload, received_at)
	VALUES
		(:id, :source_id, :org_id, :project_id, :kind, :record_index, :reason, :payload, :received_at)`

	rows := make([]rejectDB, 0, len(rejects))
	for _, r := range rejects {
		rows = append(rows, toDB(r))
	}

	if _, err := s.db.NamedExecContext(ctx, q, rows); err != nil {
		return 0, fmt.Errorf("namedexeccontext: %w", err)
	}

	return len(rows), nil
}

// CountSince reports how many rejects a source already has stored in the
// window, which is what the hourly cap is measured against.
func (s *Store) CountSince(ctx context.Context, sourceID uuid.UUID, since time.Time) (int, error) {
	const q = `SELECT count(1) FROM ingest_rejects WHERE source_id = $1 AND received_at >= $2`

	var n int
	if err := s.db.GetContext(ctx, &n, q, sourceID, since.UTC()); err != nil {
		return 0, fmt.Errorf("getcontext: %w", err)
	}

	return n, nil
}

// Query lists rejects matching the filter, newest first.
func (s *Store) Query(ctx context.Context, f rejectbus.Filter) ([]rejectbus.Reject, error) {
	data := map[string]any{
		"org_id": f.OrgID.String(),
		"limit":  f.Limit,
	}

	buf := bytes.NewBufferString(`SELECT ` + columns + ` FROM ingest_rejects WHERE org_id = :org_id`)

	if f.ProjectID != nil {
		data["project_id"] = f.ProjectID.String()
		buf.WriteString(` AND project_id = :project_id`)
	}
	if f.SourceID != nil {
		data["source_id"] = f.SourceID.String()
		buf.WriteString(` AND source_id = :source_id`)
	}
	if f.Kind != "" {
		data["kind"] = f.Kind
		buf.WriteString(` AND kind = :kind`)
	}
	if f.Since != nil {
		data["since"] = f.Since.UTC()
		buf.WriteString(` AND received_at >= :since`)
	}

	buf.WriteString(` ORDER BY received_at DESC, id DESC LIMIT :limit`)

	var rows []rejectDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]rejectbus.Reject, len(rows))
	for i, r := range rows {
		out[i] = toBus(r)
	}

	return out, nil
}

// CountByKind totals an org's stored rejects per kind.
func (s *Store) CountByKind(ctx context.Context, orgID uuid.UUID, since time.Time) (map[string]int64, error) {
	const q = `
	SELECT kind, count(1) AS n
	FROM ingest_rejects
	WHERE org_id = $1 AND received_at >= $2
	GROUP BY kind`

	var rows []struct {
		Kind string `db:"kind"`
		N    int64  `db:"n"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, orgID, since.UTC()); err != nil {
		return nil, fmt.Errorf("selectcontext: %w", err)
	}

	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Kind] = r.N
	}

	return out, nil
}
