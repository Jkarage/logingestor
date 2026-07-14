// Package sourceotel provides an otel extension for sourcebus.
package sourceotel

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/foundation/otel"
)

// Extension provides a wrapper for otel functionality around the sourcebus.
type Extension struct {
	bus sourcebus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the sourcebus with otel.
func NewExtension() sourcebus.Extension {
	return func(bus sourcebus.ExtBusiness) sourcebus.ExtBusiness {
		return &Extension{bus: bus}
	}
}

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, ns sourcebus.NewSource) (sourcebus.Source, string, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.create")
	defer span.End()
	return ext.bus.Create(ctx, actorID, ns)
}

func (ext *Extension) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]sourcebus.Source, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.querybyorg")
	defer span.End()
	return ext.bus.QueryByOrg(ctx, orgID)
}

func (ext *Extension) QueryByID(ctx context.Context, id uuid.UUID) (sourcebus.Source, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.querybyid")
	defer span.End()
	return ext.bus.QueryByID(ctx, id)
}

func (ext *Extension) QueryByKeyHash(ctx context.Context, keyHash string) (sourcebus.Source, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.querybykeyhash")
	defer span.End()
	return ext.bus.QueryByKeyHash(ctx, keyHash)
}

func (ext *Extension) Disable(ctx context.Context, actorID uuid.UUID, source sourcebus.Source) (sourcebus.Source, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.disable")
	defer span.End()
	return ext.bus.Disable(ctx, actorID, source)
}

func (ext *Extension) RotateKey(ctx context.Context, actorID uuid.UUID, source sourcebus.Source) (sourcebus.Source, string, error) {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.rotatekey")
	defer span.End()
	return ext.bus.RotateKey(ctx, actorID, source)
}

func (ext *Extension) TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error {
	ctx, span := otel.AddSpan(ctx, "business.sourcebus.touchlastseen")
	defer span.End()
	return ext.bus.TouchLastSeen(ctx, id, when)
}
