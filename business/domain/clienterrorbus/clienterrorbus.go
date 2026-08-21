package clienterrorbus

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Storer declares the persistence behavior this package needs.
//
// The event table doubles as the work queue: Ingest writes, ClaimUnprocessed
// takes a batch for grouping. There is no broker because there is nothing a
// broker would add here that a table with SKIP LOCKED does not already do, and
// an in-memory queue would make the 202 a lie the first time the process
// restarts.
type Storer interface {
	Ingest(ctx context.Context, events []Event) (int, error)

	ClaimUnprocessed(ctx context.Context, limit int, maxAttempts int) ([]Event, error)
	AttachToIssue(ctx context.Context, eventID uuid.UUID, issueID uuid.UUID, fingerprint string, version int) error
	MarkFailed(ctx context.Context, eventID uuid.UUID, reason string, maxAttempts int) error

	UpsertIssue(ctx context.Context, i Issue) (Issue, bool, error)
	RecordFacets(ctx context.Context, issueID uuid.UUID, facets map[string][]string) error
	TouchIssue(ctx context.Context, issueID uuid.UUID, at time.Time, level string, sampleEventID uuid.UUID, count int) (reopened bool, err error)

	QueryIssueByID(ctx context.Context, id uuid.UUID) (Issue, error)
	QueryIssues(ctx context.Context, f IssueFilter) ([]Issue, string, error)
	UpdateIssue(ctx context.Context, i Issue) error
	QueryIssueEvents(ctx context.Context, issueID uuid.UUID, limit int) ([]Event, error)
	QueryIssueSeries(ctx context.Context, issueID uuid.UUID, from, to time.Time, interval time.Duration) ([]Bucket, error)
	QueryStats(ctx context.Context, orgID *uuid.UUID, allOrgs bool, from, to time.Time) (Stats, error)
	PurgeOrg(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// Notifier is told about issues worth waking someone for. It is an interface so
// this package does not depend on the alerting domain: delivery, suppression
// and maintenance windows already exist and are not rebuilt here.
type Notifier interface {
	IssueOpened(ctx context.Context, i Issue, sample Event)
	IssueRegressed(ctx context.Context, i Issue, sample Event)
}

// Business manages the set of APIs for client error monitoring.
type Business struct {
	log      *logger.Logger
	storer   Storer
	notifier Notifier
}

// NewBusiness constructs a client error business API. notifier may be nil, in
// which case grouping still happens and nothing is delivered.
func NewBusiness(log *logger.Logger, storer Storer, notifier Notifier) *Business {
	return &Business{log: log, storer: storer, notifier: notifier}
}

// Ingest validates, scrubs and stores a batch, returning how many events were
// accepted. Duplicates by event id are not counted twice.
//
// Nothing here computes a fingerprint or writes an issue. The browser is waiting
// on this call, and a poisoned event must not be able to fail the request that
// delivered it — grouping happens on the worker.
func (b *Business) Ingest(ctx context.Context, who Reporter, events []NewEvent) (int, error) {
	switch {
	case len(events) == 0:
		return 0, ErrNoEvents
	case len(events) > MaxBatchEvents:
		return 0, ErrTooManyEvents
	}

	now := time.Now().UTC()

	stored := make([]Event, 0, len(events))
	for _, ne := range events {
		ne = ne.Scrub()

		if !ValidLevel(ne.Level) {
			ne.Level = LevelError
		}
		if !ValidKind(ne.Kind) {
			ne.Kind = KindManual
		}
		if ne.Name == "" {
			ne.Name = "Error"
		}
		if ne.EventID == uuid.Nil {
			// A client that omits the idempotency key gets one, so the report is
			// kept rather than dropped; a retry of it will duplicate, which is
			// better than losing the only report of a crash.
			ne.EventID = uuid.New()
		}

		occurred := ne.OccurredAt.UTC()
		if occurred.IsZero() || occurred.After(now.Add(time.Hour)) {
			// A browser clock can be anything at all. A timestamp from the future
			// would sort above every real event forever.
			occurred = now
		}

		stored = append(stored, Event{
			ID:             uuid.New(),
			EventID:        ne.EventID,
			OrgID:          who.OrgID,
			UserID:         who.UserID,
			Role:           who.Role,
			Level:          ne.Level,
			Kind:           ne.Kind,
			Name:           ne.Name,
			Message:        ne.Message,
			Stack:          ne.Stack,
			ComponentStack: ne.ComponentStack,
			Release:        ne.Release,
			Environment:    ne.Environment,
			URL:            ne.URL,
			UserAgent:      ne.UserAgent,
			API:            ne.API,
			Breadcrumbs:    ne.Breadcrumbs,
			OccurredAt:     occurred,
			ReceivedAt:     now,
			SampledCount:   ne.SampledCount,
		})
	}

	n, err := b.storer.Ingest(ctx, stored)
	if err != nil {
		return 0, fmt.Errorf("ingest: %w", err)
	}

	return n, nil
}

// maxProcessAttempts is how many times the worker will retry one event before
// setting it aside. A single event that cannot be grouped must not be able to
// hold up the queue behind it.
const maxProcessAttempts = 3

// ProcessBatch groups up to limit unprocessed events into issues and returns how
// many it handled. It is safe to run concurrently: the claim skips rows another
// worker holds.
func (b *Business) ProcessBatch(ctx context.Context, limit int) (int, error) {
	events, err := b.storer.ClaimUnprocessed(ctx, limit, maxProcessAttempts)
	if err != nil {
		return 0, fmt.Errorf("claimunprocessed: %w", err)
	}

	for _, e := range events {
		if err := b.group(ctx, e); err != nil {
			// The event is the problem, not the queue. Record the reason and move
			// on; after maxProcessAttempts the row is set aside for good.
			b.log.Error(ctx, "clienterror: grouping failed", "eventID", e.EventID, "err", err)

			if merr := b.storer.MarkFailed(ctx, e.ID, err.Error(), maxProcessAttempts); merr != nil {
				return 0, fmt.Errorf("markfailed: %w", merr)
			}
		}
	}

	return len(events), nil
}

// group assigns one event to an issue, creating the issue if this is the first
// time we have seen this fingerprint.
func (b *Business) group(ctx context.Context, e Event) error {
	ne := NewEvent{
		Kind:    e.Kind,
		Name:    e.Name,
		Message: e.Message,
		Stack:   e.Stack,
		URL:     e.URL,
		API:     e.API,
	}

	fingerprint, title, culprit := Fingerprint(ne)

	issue, created, err := b.storer.UpsertIssue(ctx, Issue{
		ID:            uuid.New(),
		OrgID:         e.OrgID,
		Fingerprint:   fingerprint,
		Title:         title,
		Culprit:       culprit,
		Level:         e.Level,
		Kind:          e.Kind,
		Status:        StatusUnresolved,
		FirstSeenAt:   e.OccurredAt,
		LastSeenAt:    e.OccurredAt,
		SampleEventID: &e.ID,
	})
	if err != nil {
		return fmt.Errorf("upsertissue: %w", err)
	}

	var reopened bool
	if !created {
		// An existing issue advances its last-seen and count. The sample is
		// replaced so the detail view shows a recent occurrence rather than the
		// first one, which may predate a dozen deploys.
		reopened, err = b.storer.TouchIssue(ctx, issue.ID, e.OccurredAt, e.Level, e.ID, e.SampledCount)
		if err != nil {
			return fmt.Errorf("touchissue: %w", err)
		}
	}

	facets := map[string][]string{}
	if e.UserID != nil {
		facets["user"] = []string{e.UserID.String()}
	}
	if e.OrgID != nil {
		facets["org"] = []string{e.OrgID.String()}
	}
	if e.Release != "" {
		facets["release"] = []string{e.Release}
	}
	if len(facets) > 0 {
		if err := b.storer.RecordFacets(ctx, issue.ID, facets); err != nil {
			return fmt.Errorf("recordfacets: %w", err)
		}
	}

	if err := b.storer.AttachToIssue(ctx, e.ID, issue.ID, fingerprint, FingerprintVersion); err != nil {
		return fmt.Errorf("attachtoissue: %w", err)
	}

	// Notify after the event is attached, so anyone following the alert into the
	// dashboard finds the issue already complete.
	if b.notifier != nil {
		switch {
		case created:
			b.notifier.IssueOpened(ctx, issue, e)
		case reopened:
			issue.Regressed = true
			b.notifier.IssueRegressed(ctx, issue, e)
		}
	}

	return nil
}

// QueryIssues lists issues matching the filter.
func (b *Business) QueryIssues(ctx context.Context, f IssueFilter) ([]Issue, string, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	switch f.Sort {
	case SortCount, SortUsers:
	default:
		f.Sort = SortLastSeen
	}

	issues, next, err := b.storer.QueryIssues(ctx, f)
	if err != nil {
		return nil, "", fmt.Errorf("queryissues: %w", err)
	}

	return issues, next, nil
}

// QueryIssueByID returns one issue.
func (b *Business) QueryIssueByID(ctx context.Context, id uuid.UUID) (Issue, error) {
	i, err := b.storer.QueryIssueByID(ctx, id)
	if err != nil {
		return Issue{}, fmt.Errorf("queryissuebyid: %w", err)
	}

	return i, nil
}

// QueryIssueEvents returns an issue's most recent events.
func (b *Business) QueryIssueEvents(ctx context.Context, issueID uuid.UUID, limit int) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	events, err := b.storer.QueryIssueEvents(ctx, issueID, limit)
	if err != nil {
		return nil, fmt.Errorf("queryissueevents: %w", err)
	}

	return events, nil
}

// QueryIssueSeries returns an issue's count over time.
func (b *Business) QueryIssueSeries(ctx context.Context, issueID uuid.UUID, from, to time.Time, interval time.Duration) ([]Bucket, error) {
	if interval <= 0 {
		interval = time.Hour
	}

	buckets, err := b.storer.QueryIssueSeries(ctx, issueID, from, to, interval)
	if err != nil {
		return nil, fmt.Errorf("queryissueseries: %w", err)
	}

	return buckets, nil
}

// UpdateIssue applies a triage decision.
//
// Resolving clears the regressed flag: the point of the flag is to say "this
// came back after you closed it", and closing it again is the acknowledgement.
func (b *Business) UpdateIssue(ctx context.Context, i Issue, ui UpdateIssue) (Issue, error) {
	if ui.Status != nil {
		status, err := ParseStatus(*ui.Status)
		if err != nil {
			return Issue{}, err
		}

		if status != i.Status {
			now := time.Now()
			switch status {
			case StatusResolved:
				i.ResolvedAt = &now
				i.Regressed = false
			default:
				i.ResolvedAt = nil
			}
		}
		i.Status = status
	}

	if ui.AssigneeID != nil {
		i.AssigneeID = *ui.AssigneeID
	}

	if err := b.storer.UpdateIssue(ctx, i); err != nil {
		return Issue{}, fmt.Errorf("updateissue: %w", err)
	}

	return i, nil
}

// QueryStats returns the dashboard tiles for a window.
func (b *Business) QueryStats(ctx context.Context, orgID *uuid.UUID, allOrgs bool, from, to time.Time) (Stats, error) {
	s, err := b.storer.QueryStats(ctx, orgID, allOrgs, from, to)
	if err != nil {
		return Stats{}, fmt.Errorf("querystats: %w", err)
	}

	return s, nil
}

// PurgeOrg deletes every client error record belonging to an org, for a deletion
// request. Issues cascade from the org, taking their facets with them.
func (b *Business) PurgeOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	n, err := b.storer.PurgeOrg(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("purgeorg: %w", err)
	}

	b.log.Info(ctx, "clienterror: purged org", "orgID", orgID, "rows", n)

	return n, nil
}
