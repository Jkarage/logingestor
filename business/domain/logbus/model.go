// Package logbus provides business access to the log domain.
package logbus

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound     = errors.New("log not found")
	ErrInvalidLevel = errors.New("invalid log level")
)

// Level represents a log severity level.
type Level struct{ value string }

func (l Level) String() string      { return l.value }
func (l Level) Equal(l2 Level) bool { return l.value == l2.value }

var (
	LevelDebug = Level{"DEBUG"}
	LevelInfo  = Level{"INFO"}
	LevelWarn  = Level{"WARN"}
	LevelError = Level{"ERROR"}
)

var levels = map[string]Level{
	"DEBUG": LevelDebug,
	"INFO":  LevelInfo,
	"WARN":  LevelWarn,
	"ERROR": LevelError,
}

// ParseLevel parses the string value into a Level.
func ParseLevel(s string) (Level, error) {
	l, ok := levels[s]
	if !ok {
		return Level{}, fmt.Errorf("%w: %q", ErrInvalidLevel, s)
	}
	return l, nil
}

// Source type values distinguish application logs from infrastructure logs.
const (
	SourceTypeApp   = "app"
	SourceTypeInfra = "infra"
)

// Infra holds the infrastructure dimensions of a log entry. All fields are
// optional and only populated for source_type='infra' entries.
type Infra struct {
	Host            string
	Container       string
	Pod             string
	Namespace       string
	Cluster         string
	Unit            string
	Facility        string
	Region          string
	CloudResourceID string
}

// Log is a single log entry.
type Log struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	Level      Level
	Message    string
	Source     string
	Timestamp  time.Time
	Tags       []string
	Meta       map[string]any
	SourceType string
	SourceID   *uuid.UUID
	Infra      Infra
	Attributes map[string]any
}

// NewLog contains the data needed to create a log entry.
type NewLog struct {
	ProjectID  uuid.UUID
	Level      Level
	Message    string
	Source     string
	Timestamp  time.Time
	Tags       []string
	Meta       map[string]any
	SourceType string
	SourceID   *uuid.UUID
	Infra      Infra
	Attributes map[string]any
}

// TotalMode selects how (or whether) a log query computes its total row count.
// An exact count over a project with tens of millions of rows forces a
// sequential scan and costs ~10s, so it is never the default.
type TotalMode int

const (
	// TotalBounded counts matching rows up to TotalCap and reports whether the
	// cap was hit. This is the default and stays index-only.
	TotalBounded TotalMode = iota

	// TotalNone skips the count entirely.
	TotalNone

	// TotalExact runs a true count(1). Only for explicit opt-in; on a large
	// project this is a multi-second sequential scan.
	TotalExact
)

// TotalCap bounds a TotalBounded count. Callers should render a capped total as
// "10,000+" rather than an exact figure.
const TotalCap = 10000

// QueryFilter holds filters for a log query.
type QueryFilter struct {
	ProjectID uuid.UUID

	// ProjectIDs selects the org-wide read path across several projects. When
	// set it replaces ProjectID, and the store fans out per project.
	ProjectIDs []uuid.UUID

	// Levels matches any of the given levels. Empty means all.
	Levels []Level

	// Search matches the message or source, case-insensitively.
	Search *string

	// Source matches the emitting service exactly.
	Source *string

	// Tags requires every listed tag to be present on the row.
	Tags []string

	// Meta requires every listed key to equal its value in the structured meta
	// field. Matching is JSONB containment, so values compare as JSON strings —
	// meta.orderId=123 matches {"orderId":"123"}, not {"orderId":123}.
	Meta map[string]string

	From       *time.Time
	To         *time.Time
	SourceType *string
	TotalMode  TotalMode
}

// scanFilters reports whether the filter needs a window bound because no plan
// reliably answers it.
//
// Only tags qualifies now. Search, source, level and meta all became unbounded
// once their indexes were built — measured over the full retained range with
// values that match nothing, the worst case: text search 12.9ms via a BitmapOr
// across the message and source trigram indexes, source 0.1ms, level 0.1ms,
// meta 2.2ms via the JSONB GIN index.
//
// tags is the exception, and not for want of an index. Postgres estimates ~24k
// matches for an array containment that yields zero, so it picks the ordered
// (project_id, ts DESC) walk and scans the project — 19.5s. Forcing the GIN
// path with an OFFSET 0 fence inverts the problem rather than solving it: 1.7ms
// when the tag is absent, but 26s when the tag matches 287k rows, where the
// ordered walk takes 9ms. Neither shape is safe for both, and the estimate is
// what would have to improve. Raising the statistics target on tags did not
// move it.
//
// sourceType is excluded: logs_project_sourcetype_ts_idx covers it directly.
func (f QueryFilter) scanFilters() bool {
	return len(f.Tags) > 0
}

// applyScanWindow bounds a query that uses an unindexed predicate. Callers that
// pass no lower bound get one; a range wider than MaxRawWindow is refused rather
// than served slowly.
//
// This constraint disappears once message and tags are GIN-indexed.
func (f *QueryFilter) applyScanWindow(now time.Time) error {
	if !f.scanFilters() {
		return nil
	}

	to := now
	if f.To != nil {
		to = *f.To
	}

	if f.From == nil {
		from := to.Add(-MaxRawWindow)
		f.From = &from
		return nil
	}

	if to.Sub(*f.From) > MaxRawWindow {
		return ErrWindowTooWide
	}

	return nil
}

// QueryResult holds a page of log results. Total is nil when the caller asked
// for TotalNone; TotalIsExact is false when the count hit TotalCap.
type QueryResult struct {
	Logs         []Log
	NextCursor   *string
	Total        *int
	TotalIsExact bool
}
