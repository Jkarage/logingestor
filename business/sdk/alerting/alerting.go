// Package alerting wires the log store to threshold alert evaluation.
//
// It exists to keep integrationbus and logbus independent of one another: the
// log domain already reaches into alerting through the logalert extension, so a
// direct dependency the other way would be a cycle. This adapter sits above both
// and belongs to neither.
package alerting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// MaxThresholdCount caps a single evaluation's count. A predicate matching nearly
// everything must not turn one pass into a full project scan, and any count at or
// above the cap already satisfies every threshold below it.
const MaxThresholdCount = 250_000

// LogCounter is the slice of the log store this adapter needs.
type LogCounter interface {
	CountMatching(ctx context.Context, projectID uuid.UUID, levels []string, contains, source string, from, to time.Time, cap int) (int, error)
}

// Counter adapts a log store to integrationbus.ThresholdCounter.
type Counter struct {
	logs LogCounter
}

// NewCounter constructs the adapter.
func NewCounter(logs LogCounter) *Counter {
	return &Counter{logs: logs}
}

// CountMatching implements integrationbus.ThresholdCounter.
func (c *Counter) CountMatching(ctx context.Context, projectID uuid.UUID, q integrationbus.Query, from, to time.Time) (int, error) {
	n, err := c.logs.CountMatching(ctx, projectID, q.Levels, q.Contains, q.Source, from, to, MaxThresholdCount)
	if err != nil {
		return 0, fmt.Errorf("countmatching: %w", err)
	}
	return n, nil
}
