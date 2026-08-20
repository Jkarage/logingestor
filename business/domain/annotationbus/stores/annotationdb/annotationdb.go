// Package annotationdb contains annotation database access.
package annotationdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages annotation data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

type annotationDB struct {
	ID          uuid.UUID  `db:"id"`
	OrgID       uuid.UUID  `db:"org_id"`
	ProjectID   uuid.UUID  `db:"project_id"`
	LogID       *uuid.UUID `db:"log_id"`
	TS          time.Time  `db:"ts"`
	Body        string     `db:"body"`
	CreatedBy   uuid.UUID  `db:"created_by"`
	DateCreated time.Time  `db:"date_created"`
	DateUpdated time.Time  `db:"date_updated"`
}

func toDB(a annotationbus.Annotation) annotationDB {
	return annotationDB{
		ID: a.ID, OrgID: a.OrgID, ProjectID: a.ProjectID, LogID: a.LogID,
		TS: a.TS.UTC(), Body: a.Body, CreatedBy: a.CreatedBy,
		DateCreated: a.DateCreated.UTC(), DateUpdated: a.DateUpdated.UTC(),
	}
}

func toBus(db annotationDB) annotationbus.Annotation {
	return annotationbus.Annotation{
		ID: db.ID, OrgID: db.OrgID, ProjectID: db.ProjectID, LogID: db.LogID,
		TS: db.TS.UTC(), Body: db.Body, CreatedBy: db.CreatedBy,
		DateCreated: db.DateCreated.In(time.Local), DateUpdated: db.DateUpdated.In(time.Local),
	}
}

const columns = `id, org_id, project_id, log_id, ts, body, created_by,
	date_created, date_updated`

// Create inserts an annotation.
func (s *Store) Create(ctx context.Context, a annotationbus.Annotation) error {
	const q = `
	INSERT INTO log_annotations
		(id, org_id, project_id, log_id, ts, body, created_by, date_created, date_updated)
	VALUES
		(:id, :org_id, :project_id, :log_id, :ts, :body, :created_by, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDB(a)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update writes an existing annotation. Only the body and its timestamp change;
// the anchor is fixed at creation.
func (s *Store) Update(ctx context.Context, a annotationbus.Annotation) error {
	const q = `
	UPDATE log_annotations SET
		body = :body,
		date_updated = :date_updated
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDB(a)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes an annotation.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `DELETE FROM log_annotations WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryByID returns one annotation.
func (s *Store) QueryByID(ctx context.Context, id uuid.UUID) (annotationbus.Annotation, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `SELECT ` + columns + ` FROM log_annotations WHERE id = :id`

	var db annotationDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return annotationbus.Annotation{}, fmt.Errorf("db: %w", annotationbus.ErrNotFound)
		}
		return annotationbus.Annotation{}, fmt.Errorf("db: %w", err)
	}

	return toBus(db), nil
}

// Query lists annotations matching the filter, newest first.
//
// Reads are served by log_annotations_project_ts_idx (project_id, ts DESC), so
// the common case — one project, a chart's time window — is an index range scan.
func (s *Store) Query(ctx context.Context, f annotationbus.Filter) ([]annotationbus.Annotation, error) {
	ids := make(dbarray.String, len(f.ProjectIDs))
	for i, id := range f.ProjectIDs {
		ids[i] = id.String()
	}

	data := map[string]any{
		"project_ids": ids,
		"limit":       f.Limit,
	}

	buf := bytes.NewBufferString(`SELECT ` + columns + `
	FROM log_annotations
	WHERE project_id = ANY(CAST(:project_ids AS uuid[]))`)

	if f.LogID != nil {
		data["log_id"] = f.LogID.String()
		buf.WriteString(` AND log_id = :log_id`)
	}
	if f.From != nil {
		data["from"] = f.From.UTC()
		buf.WriteString(` AND ts >= :from`)
	}
	if f.To != nil {
		data["to"] = f.To.UTC()
		buf.WriteString(` AND ts < :to`)
	}

	buf.WriteString(` ORDER BY ts DESC, id DESC LIMIT :limit`)

	var rows []annotationDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]annotationbus.Annotation, len(rows))
	for i, r := range rows {
		out[i] = toBus(r)
	}

	return out, nil
}
