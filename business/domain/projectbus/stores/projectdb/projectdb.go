// Package projectdb contains project related CRUD functionality.
package projectdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for project database access.
type Store struct {
	log *logger.Logger
	db  sqlx.ExtContext
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{
		log: log,
		db:  db,
	}
}

// NewWithTx constructs a new Store value replacing the sqlx DB
// value with a sqlx DB value that is currently inside a transaction.
func (s *Store) NewWithTx(tx sqldb.CommitRollbacker) (projectbus.Storer, error) {
	ec, err := sqldb.GetExtContext(tx)
	if err != nil {
		return nil, err
	}

	return &Store{
		log: s.log,
		db:  ec,
	}, nil
}

// Create inserts a new project into the database.
func (s *Store) Create(ctx context.Context, project projectbus.Project) error {
	const q = `
	INSERT INTO projects
		(id, org_id, name, color, date_created, date_updated)
	VALUES
		(:id, :org_id, :name, :color, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBProject(project)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", projectbus.ErrDuplicateName)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update replaces a project in the database.
func (s *Store) Update(ctx context.Context, project projectbus.Project) error {
	const q = `
	UPDATE projects
	SET
		name         = :name,
		color        = :color,
		date_updated = :date_updated
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBProject(project)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes a project from the database.
func (s *Store) Delete(ctx context.Context, project projectbus.Project) error {
	const q = `
	DELETE FROM projects
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBProject(project)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Query retrieves a list of projects from the database.
func (s *Store) Query(ctx context.Context, filter projectbus.QueryFilter, orderBy order.By, page page.Page) ([]projectbus.Project, error) {
	data := map[string]any{
		"offset":        (page.Number() - 1) * page.RowsPerPage(),
		"rows_per_page": page.RowsPerPage(),
	}

	const q = `
	SELECT
		id, org_id, name, color, date_created, date_updated
	FROM
		projects`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	orderByClause, err := orderByClause(orderBy)
	if err != nil {
		return nil, err
	}

	buf.WriteString(orderByClause)
	buf.WriteString(" OFFSET :offset ROWS FETCH NEXT :rows_per_page ROWS ONLY")

	var dbProjects []projectDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &dbProjects); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusProjects(dbProjects), nil
}

// Count returns the total number of projects matching the filter.
func (s *Store) Count(ctx context.Context, filter projectbus.QueryFilter) (int, error) {
	data := map[string]any{}

	const q = `
	SELECT count(1)
	FROM projects`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	var count struct {
		Count int `db:"count"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, buf.String(), data, &count); err != nil {
		return 0, fmt.Errorf("db: %w", err)
	}

	return count.Count, nil
}

// QueryByID gets the specified project from the database.
func (s *Store) QueryByID(ctx context.Context, projectID uuid.UUID) (projectbus.Project, error) {
	data := struct {
		ID string `db:"id"`
	}{
		ID: projectID.String(),
	}

	const q = `
	SELECT
		id, org_id, name, color, date_created, date_updated
	FROM
		projects
	WHERE
		id = :id`

	var dbProject projectDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbProject); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return projectbus.Project{}, fmt.Errorf("db: %w", projectbus.ErrNotFound)
		}
		return projectbus.Project{}, fmt.Errorf("db: %w", err)
	}

	return toBusProject(dbProject), nil
}

// QueryAccessible returns only the projects within an org that the given user can see.
func (s *Store) QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]projectbus.Project, error) {
	data := struct {
		OrgID  string `db:"org_id"`
		UserID string `db:"user_id"`
	}{
		OrgID:  orgID.String(),
		UserID: userID.String(),
	}

	const q = `
	SELECT p.id, p.org_id, p.name, p.color, p.date_created, p.date_updated
	FROM projects p
	WHERE p.org_id = :org_id
	AND (
		EXISTS (
			SELECT 1 FROM org_members m
			WHERE m.org_id = p.org_id
			  AND m.user_id = :user_id
			  AND m.role IN ('ORG ADMIN', 'SUPER ADMIN')
		)
		OR
		EXISTS (
			SELECT 1 FROM user_project_access upa
			WHERE upa.project_id = p.id
			  AND upa.user_id = :user_id
		)
	)
	ORDER BY p.date_created ASC`

	var dbProjects []projectDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbProjects); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusProjects(dbProjects), nil
}
