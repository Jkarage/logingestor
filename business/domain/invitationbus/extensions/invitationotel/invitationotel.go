// Package invitationotel provides an extension for invitationbus that adds otel tracking.
package invitationotel

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/otel"
)

// Extension provides a wrapper for otel functionality around the invitationbus.
type Extension struct {
	bus invitationbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the invitationbus with otel.
func NewExtension() invitationbus.Extension {
	return func(bus invitationbus.ExtBusiness) invitationbus.ExtBusiness {
		return &Extension{bus: bus}
	}
}

func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (invitationbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, ni invitationbus.NewInvitation) (invitationbus.Invitation, error) {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.create")
	defer span.End()
	return ext.bus.Create(ctx, actorID, ni)
}

func (ext *Extension) Revoke(ctx context.Context, actorID uuid.UUID, invID uuid.UUID) error {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.revoke")
	defer span.End()
	return ext.bus.Revoke(ctx, actorID, invID)
}

func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]invitationbus.Invitation, error) {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.querybyorg")
	defer span.End()
	return ext.bus.QueryByOrg(ctx, orgID)
}

func (ext *Extension) QueryByToken(ctx context.Context, token string) (invitationbus.Invitation, error) {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.querybytoken")
	defer span.End()
	return ext.bus.QueryByToken(ctx, token)
}

func (ext *Extension) QueryByID(ctx context.Context, invID uuid.UUID) (invitationbus.Invitation, error) {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.querybyid")
	defer span.End()
	return ext.bus.QueryByID(ctx, invID)
}

func (ext *Extension) MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error {
	ctx, span := otel.AddSpan(ctx, "business.invitationbus.markaccepted")
	defer span.End()
	return ext.bus.MarkAccepted(ctx, invID, acceptedAt)
}
