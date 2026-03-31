// Package invitationaudit provides an extension for invitationbus that adds audit logging.
package invitationaudit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jkarage/logingestor/business/types/name"
)

// Extension provides a wrapper for audit functionality around the invitationbus.
type Extension struct {
	bus      invitationbus.ExtBusiness
	auditBus auditbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the invitationbus with audit.
func NewExtension(auditBus auditbus.ExtBusiness) invitationbus.Extension {
	return func(bus invitationbus.ExtBusiness) invitationbus.ExtBusiness {
		return &Extension{bus: bus, auditBus: auditBus}
	}
}

func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (invitationbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, ni invitationbus.NewInvitation) (invitationbus.Invitation, error) {
	inv, err := ext.bus.Create(ctx, actorID, ni)
	if err != nil {
		return invitationbus.Invitation{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		ObjID:     inv.ID,
		ObjDomain: domain.Invitation,
		ObjName:   name.Name{},
		ActorID:   actorID,
		Action:    "created",
		Data:      ni,
		Message:   "invitation sent to " + ni.Email,
	}); err != nil {
		return invitationbus.Invitation{}, err
	}

	return inv, nil
}

func (ext *Extension) Revoke(ctx context.Context, actorID uuid.UUID, invID uuid.UUID) error {
	if err := ext.bus.Revoke(ctx, actorID, invID); err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		ObjID:     invID,
		ObjDomain: domain.Invitation,
		ObjName:   name.Name{},
		ActorID:   actorID,
		Action:    "revoked",
		Data:      nil,
		Message:   "invitation revoked",
	}); err != nil {
		return err
	}

	return nil
}

func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]invitationbus.Invitation, error) {
	return ext.bus.QueryByOrg(ctx, orgID)
}

func (ext *Extension) QueryByToken(ctx context.Context, token string) (invitationbus.Invitation, error) {
	return ext.bus.QueryByToken(ctx, token)
}

func (ext *Extension) QueryByID(ctx context.Context, invID uuid.UUID) (invitationbus.Invitation, error) {
	return ext.bus.QueryByID(ctx, invID)
}

func (ext *Extension) MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error {
	return ext.bus.MarkAccepted(ctx, invID, acceptedAt)
}
