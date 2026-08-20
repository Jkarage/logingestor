package viewdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jmoiron/sqlx/types"
)

type permalinkDB struct {
	ID          uuid.UUID      `db:"id"`
	OrgID       uuid.UUID      `db:"org_id"`
	ProjectID   *uuid.UUID     `db:"project_id"`
	Slug        string         `db:"slug"`
	Kind        string         `db:"kind"`
	LogID       *uuid.UUID     `db:"log_id"`
	Query       types.JSONText `db:"query"`
	CreatedBy   uuid.UUID      `db:"created_by"`
	DateCreated time.Time      `db:"date_created"`
}

func toDBPermalink(p viewbus.Permalink) permalinkDB {
	return permalinkDB{
		ID: p.ID, OrgID: p.OrgID, ProjectID: p.ProjectID,
		Slug: p.Slug, Kind: p.Kind, LogID: p.LogID,
		Query:     types.JSONText(p.Query),
		CreatedBy: p.CreatedBy, DateCreated: p.DateCreated.UTC(),
	}
}

func toBusPermalink(db permalinkDB) viewbus.Permalink {
	return viewbus.Permalink{
		ID: db.ID, OrgID: db.OrgID, ProjectID: db.ProjectID,
		Slug: db.Slug, Kind: db.Kind, LogID: db.LogID,
		Query:     json.RawMessage(db.Query),
		CreatedBy: db.CreatedBy, DateCreated: db.DateCreated.In(time.Local),
	}
}

const permalinkColumns = `id, org_id, project_id, slug, kind, log_id, query,
	created_by, date_created`

// CreatePermalink inserts a permalink. A slug collision is reported as
// ErrSlugTaken so the business layer can mint another one.
func (s *Store) CreatePermalink(ctx context.Context, p viewbus.Permalink) error {
	const q = `
	INSERT INTO log_permalinks
		(id, org_id, project_id, slug, kind, log_id, query, created_by, date_created)
	VALUES
		(:id, :org_id, :project_id, :slug, :kind, :log_id, :query, :created_by, :date_created)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBPermalink(p)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return viewbus.ErrSlugTaken
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryPermalinkBySlug resolves a slug to its permalink.
func (s *Store) QueryPermalinkBySlug(ctx context.Context, slug string) (viewbus.Permalink, error) {
	data := struct {
		Slug string `db:"slug"`
	}{Slug: slug}

	const q = `SELECT ` + permalinkColumns + ` FROM log_permalinks WHERE slug = :slug`

	var db permalinkDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return viewbus.Permalink{}, fmt.Errorf("db: %w", viewbus.ErrNotFound)
		}
		return viewbus.Permalink{}, fmt.Errorf("db: %w", err)
	}

	return toBusPermalink(db), nil
}

// QueryPermalinksByOrg returns an org's permalinks, newest first.
func (s *Store) QueryPermalinksByOrg(ctx context.Context, orgID uuid.UUID) ([]viewbus.Permalink, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + permalinkColumns + `
	FROM log_permalinks WHERE org_id = :org_id ORDER BY date_created DESC`

	var rows []permalinkDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]viewbus.Permalink, len(rows))
	for i, r := range rows {
		out[i] = toBusPermalink(r)
	}

	return out, nil
}

// DeletePermalink removes a permalink.
func (s *Store) DeletePermalink(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `DELETE FROM log_permalinks WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}
