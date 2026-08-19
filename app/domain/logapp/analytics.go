package logapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// TimeseriesBucket is one point in the response series.
type TimeseriesBucket struct {
	TS     string         `json:"ts"`
	Counts map[string]int `json:"counts"`
}

// TimeseriesResponse is returned by GET /projects/{project_id}/logs/timeseries.
type TimeseriesResponse struct {
	Interval string             `json:"interval"`
	From     string             `json:"from"`
	To       string             `json:"to"`
	Buckets  []TimeseriesBucket `json:"buckets"`
}

// Encode implements the encoder interface.
func (r TimeseriesResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// AggregateGroup is one aggregate result row.
type AggregateGroup struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// AggregateResponse is returned by GET /projects/{project_id}/logs/aggregate.
type AggregateResponse struct {
	GroupBy string           `json:"groupBy"`
	From    string           `json:"from"`
	To      string           `json:"to"`
	Groups  []AggregateGroup `json:"groups"`
}

// Encode implements the encoder interface.
func (r AggregateResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// timeseries returns bucketed level counts for a project.
// GET /v1/projects/{project_id}/logs/timeseries?from&to&interval&level&source_type
func (a *app) timeseries(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	q := r.URL.Query()

	interval, err := logbus.ParseInterval(orDefault(q.Get("interval"), "1h"))
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	from, to, errResp := parseWindow(q)
	if errResp != nil {
		return errResp
	}

	req := logbus.TimeseriesRequest{
		ProjectID: projectID,
		From:      from,
		To:        to,
		Interval:  interval,
	}

	if req.Level, errResp = parseOptionalLevel(q); errResp != nil {
		return errResp
	}
	if req.SourceType, errResp = parseOptionalSourceType(q); errResp != nil {
		return errResp
	}

	buckets, err := a.logBus.Timeseries(ctx, req)
	if err != nil {
		if resp := analyticsErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "timeseries: %s", err)
	}

	out := make([]TimeseriesBucket, len(buckets))
	for i, b := range buckets {
		out[i] = TimeseriesBucket{TS: b.TS.Format(time.RFC3339), Counts: b.Counts}
	}

	return TimeseriesResponse{
		Interval: interval.String(),
		From:     req.From.Format(time.RFC3339),
		To:       req.To.Format(time.RFC3339),
		Buckets:  out,
	}
}

// aggregate returns the top groups for a dimension.
// GET /v1/projects/{project_id}/logs/aggregate?groupBy&from&to&limit&level&source_type
func (a *app) aggregate(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	q := r.URL.Query()

	groupBy, err := logbus.ParseGroupBy(orDefault(q.Get("groupBy"), "level"))
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	from, to, errResp := parseWindow(q)
	if errResp != nil {
		return errResp
	}

	var limit int
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		limit = n
	}

	req := logbus.AggregateRequest{
		ProjectID: projectID,
		From:      from,
		To:        to,
		GroupBy:   groupBy,
		Limit:     limit,
	}

	if req.Level, errResp = parseOptionalLevel(q); errResp != nil {
		return errResp
	}
	if req.SourceType, errResp = parseOptionalSourceType(q); errResp != nil {
		return errResp
	}

	groups, err := a.logBus.Aggregate(ctx, req)
	if err != nil {
		if resp := analyticsErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "aggregate: %s", err)
	}

	out := make([]AggregateGroup, len(groups))
	for i, g := range groups {
		out[i] = AggregateGroup{Key: g.Key, Count: g.Count}
	}

	return AggregateResponse{
		GroupBy: groupBy.String(),
		From:    req.From.Format(time.RFC3339),
		To:      req.To.Format(time.RFC3339),
		Groups:  out,
	}
}

// analyticsErr maps the logbus guardrail errors to 400 responses, returning nil
// for anything else so the caller can treat it as internal.
func analyticsErr(err error) web.Encoder {
	switch {
	case errors.Is(err, logbus.ErrInvalidRange),
		errors.Is(err, logbus.ErrTooManyBuckets),
		errors.Is(err, logbus.ErrWindowTooWide),
		errors.Is(err, logbus.ErrInvalidInterval),
		errors.Is(err, logbus.ErrInvalidGroupBy):
		return errs.New(errs.InvalidArgument, err)
	}
	return nil
}

// parseWindow reads the optional from/to pair. Absent values are left zero so
// logbus applies its own defaults.
func parseWindow(q map[string][]string) (from, to time.Time, resp web.Encoder) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}

	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"from", &from}, {"to", &to}} {
		s := get(f.name)
		if s == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, errs.Errorf(errs.InvalidArgument, "invalid '%s': want RFC3339", f.name)
		}
		*f.dst = t.UTC()
	}

	return from, to, nil
}

func parseOptionalLevel(q map[string][]string) (*logbus.Level, web.Encoder) {
	v := q["level"]
	if len(v) == 0 || v[0] == "" {
		return nil, nil
	}

	lvl, err := logbus.ParseLevel(v[0])
	if err != nil {
		return nil, errs.New(errs.InvalidArgument, err)
	}

	return &lvl, nil
}

func parseOptionalSourceType(q map[string][]string) (*string, web.Encoder) {
	v := q["source_type"]
	if len(v) == 0 {
		return nil, nil
	}

	st, err := parseSourceType(v[0])
	if err != nil {
		return nil, errs.New(errs.InvalidArgument, err)
	}

	return st, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
