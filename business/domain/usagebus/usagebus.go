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
