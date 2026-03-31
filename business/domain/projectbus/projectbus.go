// Package projectbus provides business access to the project domain.
package projectbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/delegate"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("project not found")
	ErrDuplicateName = errors.New("project name already exists in org")
)

// Storer interface declares the behavior this package needs to persist and
// retrieve data.
type Storer interface {
	NewWithTx(tx sqldb.CommitRollbacker) (Storer, error)

	Create(ctx context.Context, project Project) error
	Update(ctx context.Context, project Project) error
	Delete(ctx context.Context, project Project) error
	Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Project, error)
	Count(ctx context.Context, filter QueryFilter) (int, error)
	QueryByID(ctx context.Context, projectID uuid.UUID) (Project, error)
	QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]Project, error)
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error)

	Create(ctx context.Context, actorID uuid.UUID, np NewProject) (Project, error)
	Update(ctx context.Context, actorID uuid.UUID, project Project, up UpdateProject) (Project, error)
	Delete(ctx context.Context, actorID uuid.UUID, project Project) error
	Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Project, error)
	Count(ctx context.Context, filter QueryFilter) (int, error)
	QueryByID(ctx context.Context, projectID uuid.UUID) (Project, error)
	QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]Project, error)
}

// Extension is a function that wraps a new layer of business logic
// around the existing business logic.
type Extension func(ExtBusiness) ExtBusiness

// Business manages the set of APIs for project access.
type Business struct {
	log        *logger.Logger
	storer     Storer
	delegate   *delegate.Delegate
	extensions []Extension
}

// NewBusiness constructs a project business API for use.
func NewBusiness(log *logger.Logger, delegate *delegate.Delegate, storer Storer, extensions ...Extension) ExtBusiness {
	b := ExtBusiness(&Business{
		log:        log,
		delegate:   delegate,
		storer:     storer,
		extensions: extensions,
	})

	for i := len(extensions) - 1; i >= 0; i-- {
		ext := extensions[i]
		if ext != nil {
			b = ext(b)
		}
	}

	return b
}

// NewWithTx constructs a new business value that will use the
// specified transaction in any store related calls.
func (b *Business) NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error) {
	storer, err := b.storer.NewWithTx(tx)
	if err != nil {
		return nil, err
	}

	return NewBusiness(b.log, b.delegate, storer, b.extensions...), nil
}

// Create adds a new project to the system.
func (b *Business) Create(ctx context.Context, actorID uuid.UUID, np NewProject) (Project, error) {
	now := time.Now()

	project := Project{
		ID:          uuid.New(),
		OrgID:       np.OrgID,
		Name:        np.Name,
		Color:       np.Color,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.Create(ctx, project); err != nil {
		return Project{}, fmt.Errorf("create: %w", err)
	}

	return project, nil
}

// Update modifies information about a project.
func (b *Business) Update(ctx context.Context, actorID uuid.UUID, project Project, up UpdateProject) (Project, error) {
	if up.Name != nil {
		project.Name = *up.Name
	}
	if up.Color != nil {
		project.Color = *up.Color
	}
	project.DateUpdated = time.Now()

	if err := b.storer.Update(ctx, project); err != nil {
		return Project{}, fmt.Errorf("update: %w", err)
	}

	return project, nil
}

// Delete removes a project from the system.
func (b *Business) Delete(ctx context.Context, actorID uuid.UUID, project Project) error {
	if err := b.storer.Delete(ctx, project); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Query retrieves a list of existing projects from the database.
func (b *Business) Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Project, error) {
	projects, err := b.storer.Query(ctx, filter, orderBy, page)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return projects, nil
}

// Count returns the total number of projects.
func (b *Business) Count(ctx context.Context, filter QueryFilter) (int, error) {
	count, err := b.storer.Count(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return count, nil
}

// QueryByID finds the project identified by a given ID.
func (b *Business) QueryByID(ctx context.Context, projectID uuid.UUID) (Project, error) {
	project, err := b.storer.QueryByID(ctx, projectID)
	if err != nil {
		return Project{}, fmt.Errorf("querybyid: %w", err)
	}
	return project, nil
}

// QueryAccessible returns the projects within an org that the given user can see.
func (b *Business) QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]Project, error) {
	projects, err := b.storer.QueryAccessible(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("queryaccessible: %w", err)
	}
	return projects, nil
}
