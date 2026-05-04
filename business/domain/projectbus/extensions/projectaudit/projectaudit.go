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
		OrgID:     project.OrgID,
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   project.Name,
		ActorID:   actorID,
		Action:    "project.created",
		Data:      map[string]any{"name": project.Name, "color": project.Color},
		Message:   "project created",
	}); err != nil {
		return projectbus.Project{}, err
	}

	return project, nil
}

func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, project projectbus.Project, up projectbus.UpdateProject) (projectbus.Project, error) {
	old := project
	project, err := ext.bus.Update(ctx, actorID, project, up)
	if err != nil {
		return projectbus.Project{}, err
	}

	nameChanged := up.Name != nil
	colorChanged := up.Color != nil
	retentionChanged := up.RetentionDays != nil

	changed := 0
	if nameChanged {
		changed++
	}
	if colorChanged {
		changed++
	}
	if retentionChanged {
		changed++
	}

	action := "project.updated"
	message := "project updated"
	meta := map[string]any{}

	if changed == 1 {
		switch {
		case nameChanged:
			action = "project.renamed"
			message = "project renamed"
			meta["name"] = project.Name
			meta["old_name"] = old.Name
		case colorChanged:
			action = "project.color_updated"
			message = "project color updated"
			meta["color"] = project.Color
			meta["old_color"] = old.Color
		case retentionChanged:
			action = "project.retention_updated"
			message = "project retention updated"
			meta["retention_days"] = project.RetentionDays
			if old.RetentionDays != nil {
				meta["old_retention_days"] = old.RetentionDays
			}
		}
	} else {
		if nameChanged {
			meta["name"] = project.Name
			meta["old_name"] = old.Name
		}
		if colorChanged {
			meta["color"] = project.Color
			meta["old_color"] = old.Color
		}
		if retentionChanged {
			meta["retention_days"] = project.RetentionDays
			if old.RetentionDays != nil {
				meta["old_retention_days"] = old.RetentionDays
			}
		}
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     project.OrgID,
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   project.Name,
		ActorID:   actorID,
		Action:    action,
		Data:      meta,
		Message:   message,
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
		OrgID:     project.OrgID,
		ObjID:     project.ID,
		ObjDomain: domain.Project,
		ObjName:   project.Name,
		ActorID:   actorID,
		Action:    "project.deleted",
		Data:      map[string]any{"name": project.Name, "color": project.Color},
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

func (ext *Extension) GrantProjectAccess(ctx context.Context, actorID uuid.UUID, userID uuid.UUID, projectID uuid.UUID) error {
	if err := ext.bus.GrantProjectAccess(ctx, actorID, userID, projectID); err != nil {
		return err
	}

	project, err := ext.bus.QueryByID(ctx, projectID)
	if err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     project.OrgID,
		ObjID:     projectID,
		ObjDomain: domain.Project,
		ObjName:   project.Name,
		ActorID:   actorID,
		Action:    "project.access_granted",
		Data:      map[string]string{"user_id": userID.String()},
		Message:   "project access granted to user",
	}); err != nil {
		return err
	}

	return nil
}

// HasAccess does not apply auditing.
func (ext *Extension) HasAccess(ctx context.Context, userID uuid.UUID, projectID uuid.UUID) (bool, error) {
	return ext.bus.HasAccess(ctx, userID, projectID)
}
