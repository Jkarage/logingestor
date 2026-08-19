package logbus

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Guardrails for the analytics queries. Aggregates that can be served from the
// log_stats_hourly rollup are cheap at any window width; anything that has to
// touch logs directly is bounded, because a raw scan on the largest project
// measured ~227ms/1h, ~1.5s/6h and ~4.3s/24h.
const (
	// MaxRawWindow caps queries that must scan logs — sub-hour intervals and
	// meta.* group-bys. Two hours keeps the worst case near half a second.
	MaxRawWindow = 2 * time.Hour

	// MaxBuckets caps the number of timeseries buckets in one response.
	MaxBuckets = 1500

	// MaxAggregateLimit caps how many groups an aggregate may return.
	MaxAggregateLimit = 500

	// DefaultWindow is used when the caller supplies neither from nor to.
	DefaultWindow = 24 * time.Hour
)

// Errors returned by the analytics validators.
var (
	ErrInvalidInterval = errors.New("interval must be one of 1m, 5m, 15m, 1h, 6h, 1d")
	ErrInvalidRange    = errors.New("'from' must be before 'to'")
	ErrTooManyBuckets  = fmt.Errorf("range and interval would exceed %d buckets; widen the interval", MaxBuckets)
	ErrWindowTooWide   = fmt.Errorf("this query reads raw logs and is limited to a %s window; widen the interval or narrow the range", MaxRawWindow)
	ErrInvalidGroupBy  = errors.New("groupBy must be level, source, source_type, or meta.<field>")
)

// Interval is a validated timeseries bucket width.
type Interval struct {
	name string
	d    time.Duration
}

// String returns the wire name of the interval.
func (i Interval) String() string { return i.name }

// Duration returns the bucket width.
func (i Interval) Duration() time.Duration { return i.d }

// fromRollup reports whether this interval can be served from the hourly
// rollup. Sub-hour buckets cannot: the rollup has no finer grain.
func (i Interval) fromRollup() bool { return i.d >= time.Hour }

// The supported intervals.
var (
	Interval1m  = Interval{"1m", time.Minute}
	Interval5m  = Interval{"5m", 5 * time.Minute}
	Interval15m = Interval{"15m", 15 * time.Minute}
	Interval1h  = Interval{"1h", time.Hour}
	Interval6h  = Interval{"6h", 6 * time.Hour}
	Interval1d  = Interval{"1d", 24 * time.Hour}
)

var intervals = map[string]Interval{
	"1m": Interval1m, "5m": Interval5m, "15m": Interval15m,
	"1h": Interval1h, "6h": Interval6h, "1d": Interval1d,
}

// ParseInterval validates an interval name.
func ParseInterval(s string) (Interval, error) {
	i, ok := intervals[s]
	if !ok {
		return Interval{}, ErrInvalidInterval
	}
	return i, nil
}

// =============================================================================

// TimeseriesRequest asks for bucketed level counts over a time range.
type TimeseriesRequest struct {
	ProjectID  uuid.UUID
	From       time.Time
	To         time.Time
	Interval   Interval
	Level      *Level
	SourceType *string
}

// Bucket is one point in a timeseries. Counts always carries every level so
// clients can plot a stacked series without normalising absent keys.
type Bucket struct {
	TS     time.Time
	Counts map[string]int
}

// BucketCount is one (bucket, level) tally as the store returns it, before the
// business layer folds levels together and fills empty buckets.
type BucketCount struct {
	TS    time.Time
	Level string
	Count int
}

// LevelNames lists every level, in severity order, so a response always carries
// the same keys whether or not rows exist for them.
func LevelNames() []string {
	return []string{LevelDebug.String(), LevelInfo.String(), LevelWarn.String(), LevelError.String()}
}

// validate applies the range defaults and the guardrails.
func (r *TimeseriesRequest) validate() error {
	applyWindowDefaults(&r.From, &r.To)

	if !r.From.Before(r.To) {
		return ErrInvalidRange
	}

	if int(r.To.Sub(r.From)/r.Interval.Duration()) > MaxBuckets {
		return ErrTooManyBuckets
	}

	// Sub-hour buckets are computed from logs, so the window must stay small.
	if !r.Interval.fromRollup() && r.To.Sub(r.From) > MaxRawWindow {
		return ErrWindowTooWide
	}

	return nil
}

// =============================================================================

// metaFieldPattern bounds a meta.<field> key to a sane identifier. The field is
// always passed as a bind parameter, so this is a sanity check rather than the
// injection defence.
var metaFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)

// GroupBy names the dimension an aggregate groups on.
type GroupBy struct {
	// column is the rollup/logs column to group on, empty for meta lookups.
	column string

	// metaField is the JSONB key to group on, empty for column group-bys.
	metaField string
}

// String returns the wire form of the group-by.
func (g GroupBy) String() string {
	if g.metaField != "" {
		return "meta." + g.metaField
	}
	return g.column
}

// fromRollup reports whether this dimension exists in the hourly rollup.
func (g GroupBy) fromRollup() bool { return g.metaField == "" }

// Column returns the rollup column to group on. ok is false for a meta lookup.
// The value is always one of a closed set, never caller-supplied text, so it is
// safe to interpolate into SQL.
func (g GroupBy) Column() (string, bool) {
	return g.column, g.column != ""
}

// MetaField returns the JSONB key to group on, empty for a column group-by.
func (g GroupBy) MetaField() string { return g.metaField }

// ParseGroupBy validates a groupBy value.
func ParseGroupBy(s string) (GroupBy, error) {
	switch s {
	case "level", "source", "source_type":
		return GroupBy{column: s}, nil
	}

	if field, ok := strings.CutPrefix(s, "meta."); ok {
		if !metaFieldPattern.MatchString(field) {
			return GroupBy{}, ErrInvalidGroupBy
		}
		return GroupBy{metaField: field}, nil
	}

	return GroupBy{}, ErrInvalidGroupBy
}

// AggregateRequest asks for the top groups over a time range.
type AggregateRequest struct {
	ProjectID  uuid.UUID
	From       time.Time
	To         time.Time
	GroupBy    GroupBy
	Limit      int
	Level      *Level
	SourceType *string
}

// Group is one aggregate result row.
type Group struct {
	Key   string
	Count int
}

func (r *AggregateRequest) validate() error {
	applyWindowDefaults(&r.From, &r.To)

	if !r.From.Before(r.To) {
		return ErrInvalidRange
	}

	if r.Limit <= 0 {
		r.Limit = 50
	}
	if r.Limit > MaxAggregateLimit {
		r.Limit = MaxAggregateLimit
	}

	// meta.* group-bys have no rollup to read, so they scan logs.
	if !r.GroupBy.fromRollup() && r.To.Sub(r.From) > MaxRawWindow {
		return ErrWindowTooWide
	}

	return nil
}

// applyWindowDefaults fills in a missing from/to pair so callers may supply
// neither, either, or both.
func applyWindowDefaults(from, to *time.Time) {
	if to.IsZero() {
		*to = time.Now().UTC()
	}
	if from.IsZero() {
		*from = to.Add(-DefaultWindow)
	}
}
