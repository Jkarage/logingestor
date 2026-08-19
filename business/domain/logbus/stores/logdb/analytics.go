package logdb

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
)

// bucketRow is one (bucket, level) pair returned by a timeseries query.
type bucketRow struct {
	TS    time.Time `db:"bucket"`
	Level string    `db:"level"`
	Count int       `db:"count"`
}

// groupRow is one (key, count) pair. Key is a pointer because a meta lookup on
// a row missing the field yields SQL NULL.
type groupRow struct {
	Key   *string `db:"key"`
	Count int     `db:"count"`
}

// Timeseries returns level counts bucketed by interval.
//
// Intervals of an hour or more are summed from log_stats_hourly, which is cheap
// at any range. Sub-hour intervals have no rollup to read and so scan logs via
// logs_project_ts_idx; logbus caps that window.
func (s *Store) Timeseries(ctx context.Context, req logbus.TimeseriesRequest) ([]logbus.BucketCount, error) {
	secs := int(req.Interval.Duration().Seconds())

	data := map[string]any{
		"project_id": req.ProjectID.String(),
		"from_ts":    req.From,
		"to_ts":      req.To,
		"secs":       secs,
	}

	var buf bytes.Buffer

	if req.Interval.Duration() >= time.Hour {
		buf.WriteString(`
	SELECT
		to_timestamp(floor(extract(epoch FROM hour) / :secs) * :secs) AS bucket,
		level,
		COALESCE(sum(count), 0) AS count
	FROM log_stats_hourly
	WHERE project_id = :project_id AND hour >= :from_ts AND hour < :to_ts`)
	} else {
		buf.WriteString(`
	SELECT
		to_timestamp(floor(extract(epoch FROM ts) / :secs) * :secs) AS bucket,
		level,
		count(1) AS count
	FROM logs
	WHERE project_id = :project_id AND ts >= :from_ts AND ts < :to_ts`)
	}

	if req.Level != nil {
		data["level"] = req.Level.String()
		buf.WriteString(" AND level = :level")
	}
	if req.SourceType != nil {
		data["source_type"] = *req.SourceType
		buf.WriteString(" AND source_type = :source_type")
	}

	buf.WriteString(" GROUP BY 1, 2 ORDER BY 1")

	var rows []bucketRow
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice timeseries: %w", err)
	}

	out := make([]logbus.BucketCount, len(rows))
	for i, r := range rows {
		out[i] = logbus.BucketCount{TS: r.TS.UTC(), Level: r.Level, Count: r.Count}
	}

	return out, nil
}

// Aggregate returns the top groups for the requested dimension.
//
// level, source and source_type are summed from log_stats_hourly. A meta.<field>
// group-by has no rollup and scans logs; logbus caps that window.
func (s *Store) Aggregate(ctx context.Context, req logbus.AggregateRequest) ([]logbus.Group, error) {
	data := map[string]any{
		"project_id": req.ProjectID.String(),
		"from_ts":    req.From,
		"to_ts":      req.To,
		"limit":      req.Limit,
	}

	var buf bytes.Buffer

	if col, ok := req.GroupBy.Column(); ok {
		// col comes from a closed set validated by logbus.ParseGroupBy, never
		// from raw request text.
		fmt.Fprintf(&buf, `
	SELECT %s AS key, COALESCE(sum(count), 0) AS count
	FROM log_stats_hourly
	WHERE project_id = :project_id AND hour >= :from_ts AND hour < :to_ts`, col)
	} else {
		data["meta_key"] = req.GroupBy.MetaField()
		buf.WriteString(`
	SELECT meta ->> :meta_key AS key, count(1) AS count
	FROM logs
	WHERE project_id = :project_id AND ts >= :from_ts AND ts < :to_ts`)
	}

	if req.Level != nil {
		data["level"] = req.Level.String()
		buf.WriteString(" AND level = :level")
	}
	if req.SourceType != nil {
		data["source_type"] = *req.SourceType
		buf.WriteString(" AND source_type = :source_type")
	}

	buf.WriteString(" GROUP BY 1 ORDER BY count DESC, key ASC LIMIT :limit")

	var rows []groupRow
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &rows); err != nil {
		return nil, fmt.Errorf("namedqueryslice aggregate: %w", err)
	}

	out := make([]logbus.Group, len(rows))
	for i, r := range rows {
		key := ""
		if r.Key != nil {
			key = *r.Key
		}
		out[i] = logbus.Group{Key: key, Count: r.Count}
	}

	return out, nil
}
