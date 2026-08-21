// Package rejectbus stores a sample of the ingest records we refused.
//
// It is a dead-letter store rather than a dead-letter queue: nothing drains it
// and nothing retries from it. A rejected record is one the sender must fix, so
// the only useful thing to do with it is show it to them — and the reason a
// number alone is not enough is that a shipper runs unattended and rarely logs
// the response that carried the explanation.
package rejectbus

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Kinds of rejection. They are separate because they need different fixes: a
// parse failure is a malformed request, a validation failure is a request we
// understood and refused.
const (
	KindParse    = "parse"
	KindValidate = "validate"
)

// Limits on what is stored.
const (
	// MaxPayloadBytes bounds one stored record. Enough to see what was wrong with
	// it; not enough for the store to become a copy of the log stream.
	MaxPayloadBytes = 4 * 1024

	// MaxReasonLen bounds the message. These come from our own parser, so they
	// are short by construction.
	MaxReasonLen = 500

	// DefaultHourlyCap is how many rejects are kept per source per hour. A broken
	// shipper refuses everything at full volume; past this the records are
	// counted and dropped, because storing the flood is the failure mode this
	// store is supposed to help diagnose rather than join.
	DefaultHourlyCap = 100
)

// ErrNotFound is returned when a reject cannot be read.
var ErrNotFound = errors.New("reject not found")

// NewReject is one refused record on its way to storage.
type NewReject struct {
	SourceID    uuid.UUID
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	Kind        string
	RecordIndex int
	Reason      string
	Payload     string
}

// Reject is a stored refusal.
type Reject struct {
	ID          uuid.UUID
	SourceID    uuid.UUID
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	Kind        string
	RecordIndex int
	Reason      string
	Payload     string
	ReceivedAt  time.Time
}

// Filter narrows a listing.
type Filter struct {
	OrgID     uuid.UUID
	ProjectID *uuid.UUID
	SourceID  *uuid.UUID
	Kind      string
	Since     *time.Time
	Limit     int
}

// Scrubber removes secrets from a payload before it is stored. It is an
// interface so this package does not depend on the ingest pipeline: a rejected
// record never reaches the normaliser that would otherwise have redacted it, so
// the redaction has to happen here instead.
type Scrubber interface {
	RedactString(s string) string
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	Store(ctx context.Context, rejects []Reject) (int, error)
	CountSince(ctx context.Context, sourceID uuid.UUID, since time.Time) (int, error)
	Query(ctx context.Context, f Filter) ([]Reject, error)
	CountByKind(ctx context.Context, orgID uuid.UUID, since time.Time) (map[string]int64, error)
}

// Business manages the set of APIs for rejected records.
type Business struct {
	log       *logger.Logger
	storer    Storer
	scrubber  Scrubber
	hourlyCap int
}

// NewBusiness constructs a reject business API. A nil scrubber stores payloads
// unredacted, which is why the caller is expected to pass one.
func NewBusiness(log *logger.Logger, storer Storer, scrubber Scrubber, hourlyCap int) *Business {
	if hourlyCap <= 0 {
		hourlyCap = DefaultHourlyCap
	}

	return &Business{log: log, storer: storer, scrubber: scrubber, hourlyCap: hourlyCap}
}

// Store keeps what fits under the hourly cap and reports how many it kept.
//
// The cap is checked once for the batch rather than per record: a request either
// arrives when there is room or it does not, and a per-record check would be a
// query per record on the ingest path.
func (b *Business) Store(ctx context.Context, rejects []NewReject) (int, error) {
	if len(rejects) == 0 {
		return 0, nil
	}

	room := b.hourlyCap

	// Every record in a batch comes from one source, so one count answers for
	// all of them.
	existing, err := b.storer.CountSince(ctx, rejects[0].SourceID, hourStart(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("countsince: %w", err)
	}

	room -= existing
	if room <= 0 {
		return 0, nil
	}

	if len(rejects) > room {
		rejects = rejects[:room]
	}

	now := time.Now().UTC()
	out := make([]Reject, 0, len(rejects))

	for _, r := range rejects {
		kind := r.Kind
		if kind != KindParse && kind != KindValidate {
			kind = KindValidate
		}

		payload := r.Payload
		if b.scrubber != nil {
			payload = b.scrubber.RedactString(payload)
		}

		out = append(out, Reject{
			ID:          uuid.New(),
			SourceID:    r.SourceID,
			OrgID:       r.OrgID,
			ProjectID:   r.ProjectID,
			Kind:        kind,
			RecordIndex: r.RecordIndex,
			Reason:      truncate(r.Reason, MaxReasonLen),
			Payload:     truncate(payload, MaxPayloadBytes),
			ReceivedAt:  now,
		})
	}

	n, err := b.storer.Store(ctx, out)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// Query lists rejects matching the filter, newest first.
func (b *Business) Query(ctx context.Context, f Filter) ([]Reject, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	out, err := b.storer.Query(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	return out, nil
}

// CountByKind totals an org's stored rejects per kind since an instant, so a
// dashboard can say "mostly malformed JSON" without reading every row.
func (b *Business) CountByKind(ctx context.Context, orgID uuid.UUID, since time.Time) (map[string]int64, error) {
	out, err := b.storer.CountByKind(ctx, orgID, since)
	if err != nil {
		return nil, fmt.Errorf("countbykind: %w", err)
	}

	return out, nil
}

// HourlyCap reports the cap in force, so a reader can be told the list is a
// sample rather than left to assume it is everything.
func (b *Business) HourlyCap() int { return b.hourlyCap }

// hourStart truncates to the UTC hour the cap is counted over.
func hourStart(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// truncate bounds a stored string without splitting a rune, since Postgres
// rejects invalid UTF-8 and one bad byte would fail the whole insert.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	cut := s[:n]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}

	return cut + "…[truncated]"
}
