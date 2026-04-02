// Package projectaudit provides an extension for projectbus that adds audit logging.
package projectaudit

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jkarage/logingestor/business/types/name"
)

// Extension provides a wrapper for audit functionality around the projectbus.
type Extension struct {
	bus      projectbus.ExtBusiness
	auditBus auditbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the projectbus with audit.
func NewExtension(auditBus auditbus.ExtBusiness) projectbus.Extension {
	return func(bus projectbus.ExtBusiness) projectbus.ExtBusiness {
		return &Extension{
			bus:      bus,
			auditBus: auditBus,
		}
	}
}

// NewWithTx does not apply auditing.
func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (projectbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, np projectbus.NewProject) (projectbus.Project, error) {
	project, err := ext.bus.Create(ctx, actorID, np)
	if err != nil {
		return projectbus.Project{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   name.Name{},
		ActorID:   actorID,
		Action:    "created",
		Data:      np,
		Message:   "project created",
	}); err != nil {
		return projectbus.Project{}, err
	}

	return project, nil
}

func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, project projectbus.Project, up projectbus.UpdateProject) (projectbus.Project, error) {
	project, err := ext.bus.Update(ctx, actorID, project, up)
	if err != nil {
		return projectbus.Project{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   name.Name{},
		ActorID:   actorID,
		Action:    "updated",
		Data:      up,
		Message:   "project updated",
	}); err != nil {
		return projectbus.Project{}, err
	}

	return project, nil
}

func (ext *Extension) Delete(ctx context.Context, actorID uuid.UUID, project projectbus.Project) error {
	if err := ext.bus.Delete(ctx, actorID, project); err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   name.Name{},
		ActorID:   actorID,
		Action:    "deleted",
		Data:      nil,
		Message:   "project deleted",
	}); err != nil {
		return err
	}

	return nil
}

// Query does not apply auditing.
func (ext *Extension) Query(ctx context.Context, filter projectbus.QueryFilter, orderBy order.By, page page.Page) ([]projectbus.Project, error) {
	return ext.bus.Query(ctx, filter, orderBy, page)
}

// Count does not apply auditing.
func (ext *Extension) Count(ctx context.Context, filter projectbus.QueryFilter) (int, error) {
	return ext.bus.Count(ctx, filter)
}

// QueryByID does not apply auditing.
func (ext *Extension) QueryByID(ctx context.Context, projectID uuid.UUID) (projectbus.Project, error) {
	return ext.bus.QueryByID(ctx, projectID)
}

// QueryAccessible does not apply auditing.
func (ext *Extension) QueryAccessible(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) ([]projectbus.Project, error) {
	return ext.bus.QueryAccessible(ctx, orgID, userID)
}

// QueryByOrg does not apply auditing.
func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]projectbus.Project, error) {
	return ext.bus.QueryByOrg(ctx, orgID)
}

// GrantProjectAccess does not apply auditing.
func (ext *Extension) GrantProjectAccess(ctx context.Context, actorID uuid.UUID, userID uuid.UUID, projectID uuid.UUID) error {
	return ext.bus.GrantProjectAccess(ctx, actorID, userID, projectID)
}

// HasAccess does not apply auditing.
func (ext *Extension) HasAccess(ctx context.Context, userID uuid.UUID, projectID uuid.UUID) (bool, error) {
	return ext.bus.HasAccess(ctx, userID, projectID)
}
