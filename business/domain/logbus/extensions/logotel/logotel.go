// Package logotel provides an otel extension for logbus.
package logotel

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/otel"
)

// Extension provides a wrapper for otel functionality around the logbus.
type Extension struct {
	bus logbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the logbus with otel.
func NewExtension() logbus.Extension {
	return func(bus logbus.ExtBusiness) logbus.ExtBusiness {
		return &Extension{bus: bus}
	}
}

func (ext *Extension) BulkCreate(ctx context.Context, entries []logbus.NewLog) ([]logbus.Log, error) {
	ctx, span := otel.AddSpan(ctx, "business.logbus.bulkcreate")
	defer span.End()

	return ext.bus.BulkCreate(ctx, entries)
}

func (ext *Extension) Query(ctx context.Context, filter logbus.QueryFilter, limit int, cursor string) (logbus.QueryResult, error) {
	ctx, span := otel.AddSpan(ctx, "business.logbus.query")
	defer span.End()

	return ext.bus.Query(ctx, filter, limit, cursor)
}

func (ext *Extension) Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error) {
	ctx, span := otel.AddSpan(ctx, "business.logbus.stats")
	defer span.End()

	return ext.bus.Stats(ctx, projectID)
}
