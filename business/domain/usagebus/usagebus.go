// Package usagebus provides business access to per-source ingest usage
// counters (daily event/byte/dropped tallies) used for quota enforcement and
// as the feed the billing system will consume.
package usagebus

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Usage is a counter delta to fold into a source's daily tally.
type Usage struct {
	SourceID     uuid.UUID
	OrgID        uuid.UUID
	ProjectID    uuid.UUID
	Day          time.Time
	EventCount   int64
	ByteCount    int64
	DroppedCount int64
}

// QuotaStatus reports an org's daily infra-event quota and current usage.
type QuotaStatus struct {
	Quota int64 // -1 means unlimited
	Used  int64
}

// Exceeded reports whether the quota is finite and already met or surpassed.
func (q QuotaStatus) Exceeded() bool {
	return q.Quota >= 0 && q.Used >= q.Quota
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	Record(ctx context.Context, u Usage) error
	UsedToday(ctx context.Context, orgID uuid.UUID, day time.Time) (int64, error)
	Quota(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ExtBusiness is the public business interface.
type ExtBusiness interface {
	Record(ctx context.Context, u Usage) error
	CheckQuota(ctx context.Context, orgID uuid.UUID, day time.Time) (QuotaStatus, error)
}

// Business manages the set of APIs for usage access.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs a usage business API.
func NewBusiness(log *logger.Logger, storer Storer) ExtBusiness {
	return &Business{log: log, storer: storer}
}

// Record folds a usage delta into the source's daily tally.
func (b *Business) Record(ctx context.Context, u Usage) error {
	if err := b.storer.Record(ctx, u); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return nil
}

// CheckQuota returns the org's daily infra-event quota and today's usage.
func (b *Business) CheckQuota(ctx context.Context, orgID uuid.UUID, day time.Time) (QuotaStatus, error) {
	quota, err := b.storer.Quota(ctx, orgID)
	if err != nil {
		return QuotaStatus{}, fmt.Errorf("quota: %w", err)
	}

	used, err := b.storer.UsedToday(ctx, orgID, day)
	if err != nil {
		return QuotaStatus{}, fmt.Errorf("usedtoday: %w", err)
	}

	return QuotaStatus{Quota: quota, Used: used}, nil
}
