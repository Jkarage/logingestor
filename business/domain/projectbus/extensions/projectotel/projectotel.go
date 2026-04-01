// Package projectotel provides an extension for projectbus that adds otel tracking.
package projectotel

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/otel"
)

// Extension provides a wrapper for otel functionality around the projectbus.
type Extension struct {
	bus projectbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the projectbus with otel.
func NewExtension() projectbus.Extension {
	return func(bus projectbus.ExtBusiness) projectbus.ExtBusiness {
		return &Extension{bus: bus}
	}
}

func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (projectbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, np projectbus.NewProject) (projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.create")
	defer span.End()

	return ext.bus.Create(ctx, actorID, np)
}

func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, project projectbus.Project, up projectbus.UpdateProject) (projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.update")
	defer span.End()

	return ext.bus.Update(ctx, actorID, project, up)
}

func (ext *Extension) Delete(ctx context.Context, actorID uuid.UUID, project projectbus.Project) error {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.delete")
	defer span.End()

	return ext.bus.Delete(ctx, actorID, project)
}

func (ext *Extension) Query(ctx context.Context, filter projectbus.QueryFilter, orderBy order.By, page page.Page) ([]projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.query")
	defer span.End()

	return ext.bus.Query(ctx, filter, orderBy, page)
}

func (ext *Extension) Count(ctx context.Context, filter projectbus.QueryFilter) (int, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.count")
	defer span.End()

	return ext.bus.Count(ctx, filter)
}

func (ext *Extension) QueryByID(ctx context.Context, projectID uuid.UUID) (projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.querybyid")
	defer span.End()

	return ext.bus.QueryByID(ctx, projectID)
}

func (ext *Extension) QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.queryaccessible")
	defer span.End()

	return ext.bus.QueryAccessible(ctx, orgID, userID)
}

func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]projectbus.Project, error) {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.querybyorg")
	defer span.End()

	return ext.bus.QueryByOrg(ctx, orgID)
}

func (ext *Extension) GrantProjectAccess(ctx context.Context, actorID uuid.UUID, userID uuid.UUID, projectID uuid.UUID) error {
	ctx, span := otel.AddSpan(ctx, "business.projectbus.grantprojectaccess")
	defer span.End()

	return ext.bus.GrantProjectAccess(ctx, actorID, userID, projectID)
}
