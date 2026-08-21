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

// Usage is a counter delta to fold into a source's daily tally, and into the
// hourly counters that back source health.
type Usage struct {
	SourceID     uuid.UUID
	OrgID        uuid.UUID
	ProjectID    uuid.UUID
	Day          time.Time
	EventCount   int64
	ByteCount    int64
	DroppedCount int64

	// ErrorCount is how many of the accepted events were at ERROR. It feeds the
	// health error rate and is not part of quota accounting.
	ErrorCount int64

	// RejectCount is how many records the request refused. It is the exact
	// figure: the dead-letter store keeps only a sample, so this is what a total
	// must be read from.
	RejectCount int64
}

// SourceCounters is one source's ingest counters over a window.
type SourceCounters struct {
	Events  int64
	Errors  int64
	Dropped int64
}

// ErrorRate returns the share of events at ERROR, or zero when nothing arrived.
func (c SourceCounters) ErrorRate() float64 {
	if c.Events <= 0 {
		return 0
	}
	return float64(c.Errors) / float64(c.Events)
}

// HourCounters is one hour of a source's ingest counters. Hour is the start of
// the UTC hour the counters were folded into.
type HourCounters struct {
	Hour    time.Time
	Events  int64
	Errors  int64
	Dropped int64
}

// ProjectUsage is one project's totals over a window.
type ProjectUsage struct {
	ProjectID    uuid.UUID
	ProjectName  string
	EventCount   int64
	ByteCount    int64
	DroppedCount int64
}

// OrgUsage is an organization's usage over a window, broken down by project.
type OrgUsage struct {
	From         time.Time
	To           time.Time
	ByProject    []ProjectUsage
	EventCount   int64
	ByteCount    int64
	DroppedCount int64
}

// AppUsage is a counter delta for app-log ingestion, which has no source and is
// therefore tallied per project.
type AppUsage struct {
	ProjectID  uuid.UUID
	OrgID      uuid.UUID
	Day        time.Time
	EventCount int64
	ByteCount  int64
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
	QuerySourceCounters(ctx context.Context, sourceIDs []uuid.UUID, from time.Time) (map[uuid.UUID]SourceCounters, error)
	QuerySourceBuckets(ctx context.Context, sourceID uuid.UUID, from, to time.Time) ([]HourCounters, error)
	UsedToday(ctx context.Context, orgID uuid.UUID, day time.Time) (int64, error)
	Quota(ctx context.Context, orgID uuid.UUID) (int64, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID, from, to time.Time) ([]ProjectUsage, error)

	RecordApp(ctx context.Context, u AppUsage) error
	AppUsedToday(ctx context.Context, orgID uuid.UUID, day time.Time) (int64, error)
	AppQuota(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ExtBusiness is the public business interface.
type ExtBusiness interface {
	Record(ctx context.Context, u Usage) error
	QuerySourceCounters(ctx context.Context, sourceIDs []uuid.UUID, from time.Time) (map[uuid.UUID]SourceCounters, error)
	QuerySourceBuckets(ctx context.Context, sourceID uuid.UUID, from, to time.Time) ([]HourCounters, error)
	CheckQuota(ctx context.Context, orgID uuid.UUID, day time.Time) (QuotaStatus, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID, from, to time.Time) (OrgUsage, error)

	RecordApp(ctx context.Context, u AppUsage) error
	CheckAppQuota(ctx context.Context, orgID uuid.UUID, day time.Time) (QuotaStatus, error)
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

// QueryByOrg returns an organization's usage between from (inclusive) and to
// (exclusive), broken down by project and totalled.
//
// The window is expressed in whole UTC days because ingest_usage is a daily
// rollup — there is no finer grain to report.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID, from, to time.Time) (OrgUsage, error) {
	from = from.UTC().Truncate(24 * time.Hour)
	to = to.UTC().Truncate(24 * time.Hour)

	if !from.Before(to) {
		return OrgUsage{}, fmt.Errorf("'from' must be before 'to'")
	}

	byProject, err := b.storer.QueryByOrg(ctx, orgID, from, to)
	if err != nil {
		return OrgUsage{}, fmt.Errorf("querybyorg: %w", err)
	}

	usage := OrgUsage{From: from, To: to, ByProject: byProject}

	for _, p := range byProject {
		usage.EventCount += p.EventCount
		usage.ByteCount += p.ByteCount
		usage.DroppedCount += p.DroppedCount
	}

	return usage, nil
}

// RecordApp folds an app-log counter delta into the project's daily tally.
func (b *Business) RecordApp(ctx context.Context, u AppUsage) error {
	if err := b.storer.RecordApp(ctx, u); err != nil {
		return fmt.Errorf("recordapp: %w", err)
	}
	return nil
}

// CheckAppQuota returns the org's daily app-log event quota and today's usage.
// It is deliberately separate from the infra quota: the plans already treat app
// and infra retention as different limits.
func (b *Business) CheckAppQuota(ctx context.Context, orgID uuid.UUID, day time.Time) (QuotaStatus, error) {
	quota, err := b.storer.AppQuota(ctx, orgID)
	if err != nil {
		return QuotaStatus{}, fmt.Errorf("appquota: %w", err)
	}

	used, err := b.storer.AppUsedToday(ctx, orgID, day)
	if err != nil {
		return QuotaStatus{}, fmt.Errorf("appusedtoday: %w", err)
	}

	return QuotaStatus{Quota: quota, Used: used}, nil
}

// QuerySourceCounters totals each source's counters since from. Sources with no
// ingest in the window are absent from the map rather than present with zeros,
// so a caller can tell "nothing arrived" from "no such source".
func (b *Business) QuerySourceCounters(ctx context.Context, sourceIDs []uuid.UUID, from time.Time) (map[uuid.UUID]SourceCounters, error) {
	if len(sourceIDs) == 0 {
		return map[uuid.UUID]SourceCounters{}, nil
	}

	counters, err := b.storer.QuerySourceCounters(ctx, sourceIDs, from)
	if err != nil {
		return nil, fmt.Errorf("querysourcecounters: %w", err)
	}

	return counters, nil
}

// QuerySourceBuckets returns one source's hourly counters over [from, to).
// Empty hours are omitted; the caller fills them, since only it knows the
// bucket grid it wants to plot.
func (b *Business) QuerySourceBuckets(ctx context.Context, sourceID uuid.UUID, from, to time.Time) ([]HourCounters, error) {
	buckets, err := b.storer.QuerySourceBuckets(ctx, sourceID, from, to)
	if err != nil {
		return nil, fmt.Errorf("querysourcebuckets: %w", err)
	}

	return buckets, nil
}
