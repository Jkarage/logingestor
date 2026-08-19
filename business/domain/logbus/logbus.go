package logbus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Storer interface declares the behavior this package needs to persist and
// retrieve data.
type Storer interface {
	BulkInsert(ctx context.Context, logs []Log) error
	QueryByID(ctx context.Context, id uuid.UUID) (Log, error)
	Query(ctx context.Context, filter QueryFilter, limit int, afterTs *time.Time, afterID *uuid.UUID) ([]Log, int, error)
	Stats(ctx context.Context, projectID uuid.UUID, sourceType *string) (map[string]int, error)
	Timeseries(ctx context.Context, req TimeseriesRequest) ([]BucketCount, error)
	Aggregate(ctx context.Context, req AggregateRequest) ([]Group, error)
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	BulkCreate(ctx context.Context, entries []NewLog) ([]Log, error)
	QueryByID(ctx context.Context, id uuid.UUID) (Log, error)
	Query(ctx context.Context, filter QueryFilter, limit int, cursor string) (QueryResult, error)
	Stats(ctx context.Context, projectID uuid.UUID, sourceType *string) (map[string]int, error)
	Timeseries(ctx context.Context, req TimeseriesRequest) ([]Bucket, error)
	Aggregate(ctx context.Context, req AggregateRequest) ([]Group, error)
}

// Extension is a function that wraps a new layer of business logic
// around the existing business logic.
type Extension func(ExtBusiness) ExtBusiness

// Business manages the set of APIs for log access.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs a log business API for use.
func NewBusiness(log *logger.Logger, storer Storer, extensions ...Extension) ExtBusiness {
	b := ExtBusiness(&Business{
		log:    log,
		storer: storer,
	})

	for i := len(extensions) - 1; i >= 0; i-- {
		if ext := extensions[i]; ext != nil {
			b = ext(b)
		}
	}

	return b
}

// BulkCreate assigns IDs then persists a batch of log entries.
func (b *Business) BulkCreate(ctx context.Context, entries []NewLog) ([]Log, error) {
	logs := make([]Log, len(entries))

	for i, nl := range entries {
		tags := nl.Tags
		if tags == nil {
			tags = []string{}
		}
		meta := nl.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		attributes := nl.Attributes
		if attributes == nil {
			attributes = map[string]any{}
		}
		sourceType := nl.SourceType
		if sourceType == "" {
			sourceType = SourceTypeApp
		}

		logs[i] = Log{
			ID:         uuid.New(),
			ProjectID:  nl.ProjectID,
			Level:      nl.Level,
			Message:    nl.Message,
			Source:     nl.Source,
			Timestamp:  nl.Timestamp,
			Tags:       tags,
			Meta:       meta,
			SourceType: sourceType,
			SourceID:   nl.SourceID,
			Infra:      nl.Infra,
			Attributes: attributes,
		}
	}

	if err := b.storer.BulkInsert(ctx, logs); err != nil {
		return nil, fmt.Errorf("bulkinsert: %w", err)
	}

	return logs, nil
}

// QueryByID returns the log identified by id.
func (b *Business) QueryByID(ctx context.Context, id uuid.UUID) (Log, error) {
	l, err := b.storer.QueryByID(ctx, id)
	if err != nil {
		return Log{}, fmt.Errorf("querybyid: %w", err)
	}
	return l, nil
}

// Query returns a filtered, cursor-paginated page of logs for a project.
func (b *Business) Query(ctx context.Context, filter QueryFilter, limit int, cursorStr string) (QueryResult, error) {
	if err := filter.applyScanWindow(time.Now().UTC()); err != nil {
		return QueryResult{}, err
	}

	var afterTs *time.Time
	var afterID *uuid.UUID

	if cursorStr != "" {
		ts, id, err := decodeCursor(cursorStr)
		if err != nil {
			return QueryResult{}, fmt.Errorf("decode cursor: %w", err)
		}
		afterTs = &ts
		afterID = &id
	}

	logs, total, err := b.storer.Query(ctx, filter, limit, afterTs, afterID)
	if err != nil {
		return QueryResult{}, fmt.Errorf("query: %w", err)
	}

	var nextCursor *string
	if len(logs) == limit {
		last := logs[len(logs)-1]
		enc := encodeCursor(last.Timestamp, last.ID)
		nextCursor = &enc
	}

	result := QueryResult{
		Logs:       logs,
		NextCursor: nextCursor,
	}

	switch filter.TotalMode {
	case TotalNone:
		// Leave Total nil; the caller opted out.
	case TotalExact:
		result.Total = &total
		result.TotalIsExact = true
	default:
		result.Total = &total
		result.TotalIsExact = total < TotalCap
	}

	return result, nil
}

// Stats returns per-level counts for a project, optionally scoped to a
// source_type ("app" | "infra"); nil counts all source types.
func (b *Business) Stats(ctx context.Context, projectID uuid.UUID, sourceType *string) (map[string]int, error) {
	counts, err := b.storer.Stats(ctx, projectID, sourceType)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	return counts, nil
}

// =============================================================================
// Cursor helpers

type pageCursor struct {
	TS time.Time `json:"ts"`
	ID uuid.UUID `json:"id"`
}

func encodeCursor(ts time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(pageCursor{TS: ts.UTC(), ID: id})
	return base64.URLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("base64: %w", err)
	}
	var c pageCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("json: %w", err)
	}
	return c.TS, c.ID, nil
}

// Timeseries returns level counts bucketed by interval over the requested
// range. Every bucket in the range is present, including empty ones, and every
// bucket carries every level — so callers can plot without gap-filling.
func (b *Business) Timeseries(ctx context.Context, req TimeseriesRequest) ([]Bucket, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	rows, err := b.storer.Timeseries(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("timeseries: %w", err)
	}

	step := int64(req.Interval.Duration().Seconds())

	// Buckets are floor(epoch/step)*step in SQL, so align the first bucket the
	// same way or the series would be offset from the data.
	first := req.From.UTC().Unix() / step * step

	byTS := make(map[int64]int, 8)
	buckets := make([]Bucket, 0, 16)

	for ts := first; ts < req.To.UTC().Unix(); ts += step {
		counts := make(map[string]int, 4)
		for _, name := range LevelNames() {
			counts[name] = 0
		}

		byTS[ts] = len(buckets)
		buckets = append(buckets, Bucket{TS: time.Unix(ts, 0).UTC(), Counts: counts})
	}

	for _, r := range rows {
		i, ok := byTS[r.TS.UTC().Unix()]
		if !ok {
			// A bucket outside the generated range can only mean the row fell on
			// the exclusive upper bound; ignore rather than mis-plot it.
			continue
		}
		buckets[i].Counts[r.Level] += r.Count
	}

	return buckets, nil
}

// Aggregate returns the top groups for the requested dimension over the range.
func (b *Business) Aggregate(ctx context.Context, req AggregateRequest) ([]Group, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	groups, err := b.storer.Aggregate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", err)
	}

	return groups, nil
}
