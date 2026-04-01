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
	Query(ctx context.Context, filter QueryFilter, limit int, afterTs *time.Time, afterID *uuid.UUID) ([]Log, int, error)
	Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error)
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	BulkCreate(ctx context.Context, entries []NewLog) ([]Log, error)
	Query(ctx context.Context, filter QueryFilter, limit int, cursor string) (QueryResult, error)
	Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error)
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

		logs[i] = Log{
			ID:        uuid.New(),
			ProjectID: nl.ProjectID,
			Level:     nl.Level,
			Message:   nl.Message,
			Source:    nl.Source,
			Ts:        nl.Ts,
			Tags:      tags,
			Meta:      meta,
		}
	}

	if err := b.storer.BulkInsert(ctx, logs); err != nil {
		return nil, fmt.Errorf("bulkinsert: %w", err)
	}

	return logs, nil
}

// Query returns a filtered, cursor-paginated page of logs for a project.
func (b *Business) Query(ctx context.Context, filter QueryFilter, limit int, cursorStr string) (QueryResult, error) {
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
		enc := encodeCursor(last.Ts, last.ID)
		nextCursor = &enc
	}

	return QueryResult{
		Logs:       logs,
		NextCursor: nextCursor,
		Total:      total,
	}, nil
}

// Stats returns per-level counts for a project.
func (b *Business) Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error) {
	counts, err := b.storer.Stats(ctx, projectID)
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
