// Package sourceaudit provides an extension for sourcebus that adds audit logging.
package sourceaudit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/types/domain"
)

// Extension provides a wrapper for audit functionality around the sourcebus.
type Extension struct {
	bus      sourcebus.ExtBusiness
	auditBus auditbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the sourcebus with audit.
func NewExtension(auditBus auditbus.ExtBusiness) sourcebus.Extension {
	return func(bus sourcebus.ExtBusiness) sourcebus.ExtBusiness {
		return &Extension{
			bus:      bus,
			auditBus: auditBus,
		}
	}
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, ns sourcebus.NewSource) (sourcebus.Source, string, error) {
	source, raw, err := ext.bus.Create(ctx, actorID, ns)
	if err != nil {
		return sourcebus.Source{}, "", err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     source.OrgID,
		ObjID:     source.ID,
		ObjDomain: domain.Source,
		ObjName:   source.Name,
		ActorID:   actorID,
		Action:    "source.created",
		Data:      map[string]any{"kind": source.Kind, "project_id": source.ProjectID.String()},
		Message:   "source created",
	}); err != nil {
		return sourcebus.Source{}, "", err
	}

	return source, raw, nil
}

func (ext *Extension) Disable(ctx context.Context, actorID uuid.UUID, source sourcebus.Source) (sourcebus.Source, error) {
	source, err := ext.bus.Disable(ctx, actorID, source)
	if err != nil {
		return sourcebus.Source{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     source.OrgID,
		ObjID:     source.ID,
		ObjDomain: domain.Source,
		ObjName:   source.Name,
		ActorID:   actorID,
		Action:    "source.disabled",
		Data:      map[string]any{"kind": source.Kind},
		Message:   "source disabled",
	}); err != nil {
		return sourcebus.Source{}, err
	}

	return source, nil
}

func (ext *Extension) RotateKey(ctx context.Context, actorID uuid.UUID, source sourcebus.Source) (sourcebus.Source, string, error) {
	source, raw, err := ext.bus.RotateKey(ctx, actorID, source)
	if err != nil {
		return sourcebus.Source{}, "", err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     source.OrgID,
		ObjID:     source.ID,
		ObjDomain: domain.Source,
		ObjName:   source.Name,
		ActorID:   actorID,
		Action:    "source.key_rotated",
		Data:      map[string]any{"key_prefix": source.KeyPrefix},
		Message:   "source ingest key rotated",
	}); err != nil {
		return sourcebus.Source{}, "", err
	}

	return source, raw, nil
}

// QueryByOrg does not apply auditing.
func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]sourcebus.Source, error) {
	return ext.bus.QueryByOrg(ctx, orgID)
}

// QueryByID does not apply auditing.
func (ext *Extension) QueryByID(ctx context.Context, id uuid.UUID) (sourcebus.Source, error) {
	return ext.bus.QueryByID(ctx, id)
}

// QueryByKeyHash does not apply auditing.
func (ext *Extension) QueryByKeyHash(ctx context.Context, keyHash string) (sourcebus.Source, error) {
	return ext.bus.QueryByKeyHash(ctx, keyHash)
}

// TouchLastSeen does not apply auditing.
func (ext *Extension) TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error {
	return ext.bus.TouchLastSeen(ctx, id, when)
}
