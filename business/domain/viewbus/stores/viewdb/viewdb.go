// Package viewdb contains saved view and dashboard database access.
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
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/types"
)

// Store manages saved view and dashboard data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

// =============================================================================
// Saved views

type viewDB struct {
	ID          uuid.UUID      `db:"id"`
	OrgID       uuid.UUID      `db:"org_id"`
	ProjectID   *uuid.UUID     `db:"project_id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Query       types.JSONText `db:"query"`
	Visibility  string         `db:"visibility"`
	CreatedBy   uuid.UUID      `db:"created_by"`
	DateCreated time.Time      `db:"date_created"`
	DateUpdated time.Time      `db:"date_updated"`
}

func toDBView(v viewbus.SavedView) viewDB {
	return viewDB{
		ID: v.ID, OrgID: v.OrgID, ProjectID: v.ProjectID,
		Name: v.Name, Description: v.Description,
		Query: types.JSONText(v.Query), Visibility: v.Visibility,
		CreatedBy:   v.CreatedBy,
		DateCreated: v.DateCreated.UTC(), DateUpdated: v.DateUpdated.UTC(),
	}
}

func toBusView(db viewDB) viewbus.SavedView {
	return viewbus.SavedView{
		ID: db.ID, OrgID: db.OrgID, ProjectID: db.ProjectID,
		Name: db.Name, Description: db.Description,
		Query: json.RawMessage(db.Query), Visibility: db.Visibility,
		CreatedBy:   db.CreatedBy,
		DateCreated: db.DateCreated.In(time.Local), DateUpdated: db.DateUpdated.In(time.Local),
	}
}

const viewColumns = `id, org_id, project_id, name, description, query, visibility,
	created_by, date_created, date_updated`

// CreateView inserts a saved view.
func (s *Store) CreateView(ctx context.Context, v viewbus.SavedView) error {
	const q = `
	INSERT INTO saved_views
		(id, org_id, project_id, name, description, query, visibility, created_by,
		 date_created, date_updated)
	VALUES
		(:id, :org_id, :project_id, :name, :description, :query, :visibility, :created_by,
		 :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBView(v)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UpdateView writes an existing saved view.
func (s *Store) UpdateView(ctx context.Context, v viewbus.SavedView) error {
	const q = `
	UPDATE saved_views SET
		project_id = :project_id,
		name = :name,
		description = :description,
		query = :query,
		visibility = :visibility,
		date_updated = :date_updated
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBView(v)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// DeleteView removes a saved view.
func (s *Store) DeleteView(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, `DELETE FROM saved_views WHERE id = :id`, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryViewByID returns one saved view.
func (s *Store) QueryViewByID(ctx context.Context, id uuid.UUID) (viewbus.SavedView, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `SELECT ` + viewColumns + ` FROM saved_views WHERE id = :id`

	var db viewDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return viewbus.SavedView{}, fmt.Errorf("db: %w", viewbus.ErrNotFound)
		}
		return viewbus.SavedView{}, fmt.Errorf("db: %w", err)
	}

	return toBusView(db), nil
}

// QueryViewsByOrg returns every saved view in an org. Visibility filtering is
// the business layer's job, so one rule governs both listing and reading.
func (s *Store) QueryViewsByOrg(ctx context.Context, orgID uuid.UUID) ([]viewbus.SavedView, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + viewColumns + ` FROM saved_views WHERE org_id = :org_id ORDER BY name ASC`

	var rows []viewDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]viewbus.SavedView, len(rows))
	for i, r := range rows {
		out[i] = toBusView(r)
	}

	return out, nil
}

// =============================================================================
// Dashboards

type dashboardDB struct {
	ID          uuid.UUID      `db:"id"`
	OrgID       uuid.UUID      `db:"org_id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Panels      types.JSONText `db:"panels"`
	Visibility  string         `db:"visibility"`
	CreatedBy   uuid.UUID      `db:"created_by"`
	DateCreated time.Time      `db:"date_created"`
	DateUpdated time.Time      `db:"date_updated"`
}

func toDBDashboard(d viewbus.Dashboard) dashboardDB {
	return dashboardDB{
		ID: d.ID, OrgID: d.OrgID, Name: d.Name, Description: d.Description,
		Panels: types.JSONText(d.Panels), Visibility: d.Visibility, CreatedBy: d.CreatedBy,
		DateCreated: d.DateCreated.UTC(), DateUpdated: d.DateUpdated.UTC(),
	}
}

func toBusDashboard(db dashboardDB) viewbus.Dashboard {
	return viewbus.Dashboard{
		ID: db.ID, OrgID: db.OrgID, Name: db.Name, Description: db.Description,
		Panels: json.RawMessage(db.Panels), Visibility: db.Visibility, CreatedBy: db.CreatedBy,
		DateCreated: db.DateCreated.In(time.Local), DateUpdated: db.DateUpdated.In(time.Local),
	}
}

const dashboardColumns = `id, org_id, name, description, panels, visibility,
	created_by, date_created, date_updated`

// CreateDashboard inserts a dashboard.
func (s *Store) CreateDashboard(ctx context.Context, d viewbus.Dashboard) error {
	const q = `
	INSERT INTO dashboards
		(id, org_id, name, description, panels, visibility, created_by, date_created, date_updated)
	VALUES
		(:id, :org_id, :name, :description, :panels, :visibility, :created_by, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBDashboard(d)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UpdateDashboard writes an existing dashboard.
func (s *Store) UpdateDashboard(ctx context.Context, d viewbus.Dashboard) error {
	const q = `
	UPDATE dashboards SET
		name = :name,
		description = :description,
		panels = :panels,
		visibility = :visibility,
		date_updated = :date_updated
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBDashboard(d)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// DeleteDashboard removes a dashboard.
func (s *Store) DeleteDashboard(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, `DELETE FROM dashboards WHERE id = :id`, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryDashboardByID returns one dashboard.
func (s *Store) QueryDashboardByID(ctx context.Context, id uuid.UUID) (viewbus.Dashboard, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `SELECT ` + dashboardColumns + ` FROM dashboards WHERE id = :id`

	var db dashboardDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return viewbus.Dashboard{}, fmt.Errorf("db: %w", viewbus.ErrNotFound)
		}
		return viewbus.Dashboard{}, fmt.Errorf("db: %w", err)
	}

	return toBusDashboard(db), nil
}

// QueryDashboardsByOrg returns every dashboard in an org.
func (s *Store) QueryDashboardsByOrg(ctx context.Context, orgID uuid.UUID) ([]viewbus.Dashboard, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + dashboardColumns + ` FROM dashboards WHERE org_id = :org_id ORDER BY name ASC`

	var rows []dashboardDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]viewbus.Dashboard, len(rows))
	for i, r := range rows {
		out[i] = toBusDashboard(r)
	}

	return out, nil
}
