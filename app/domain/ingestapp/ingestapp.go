// Package ingestapp maintains the app layer api for infrastructure-log
// ingestion listeners (HTTP bulk and OTLP/HTTP).
package ingestapp

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/metrics"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/sdk/ingest"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Ingestion limits for the HTTP bulk endpoint.
const (
	maxBodyBytes  = 8 << 20 // 8 MiB
	maxRecords    = 10000
	touchInterval = 10 * time.Second // min spacing for last_seen_at updates
)

type app struct {
	log       *logger.Logger
	logBus    logbus.ExtBusiness
	sourceBus sourcebus.ExtBusiness
	usageBus  usagebus.ExtBusiness // optional; nil disables quota + usage counters
	hub       *logapp.Hub
	redactor  *ingest.Redactor
	limiter   *ingest.Limiter

	touchMu   sync.Mutex
	lastTouch map[uuid.UUID]time.Time
}

func newApp(log *logger.Logger, logBus logbus.ExtBusiness, sourceBus sourcebus.ExtBusiness, usageBus usagebus.ExtBusiness, hub *logapp.Hub) *app {
	return &app{
		log:       log,
		logBus:    logBus,
		sourceBus: sourceBus,
		usageBus:  usageBus,
		hub:       hub,
		redactor:  ingest.NewRedactor(),
		limiter:   ingest.NewLimiter(),
		lastTouch: make(map[uuid.UUID]time.Time),
	}
}

// bulk handles POST /v1/ingest/bulk — newline-delimited JSON or a JSON array of
// infrastructure-log records authenticated by a source ingest key.
func (a *app) bulk(ctx context.Context, r *http.Request) web.Encoder {
	src, err := mid.GetSource(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	body, err := readLimited(r.Body, maxBodyBytes)
	if err != nil {
		return errs.New(errs.InvalidArgument, fmt.Errorf("read body: %w", err))
	}

	parsed, parseErrs := parseRecords(r.Header.Get("Content-Type"), body)
	if len(parsed) == 0 && len(parseErrs) == 0 {
		return errs.New(errs.InvalidArgument, errors.New("request body must contain at least one record"))
	}
	if len(parsed) > maxRecords {
		return errs.Errorf(errs.InvalidArgument, "too many records: %d (max %d)", len(parsed), maxRecords)
	}

	now := time.Now().UTC()
	recErrs := append([]RecordError(nil), parseErrs...)
	recs := make([]ingest.Record, 0, len(parsed))
	for i, br := range parsed {
		rec, err := bulkToRecord(src, br, now)
		if err != nil {
			recErrs = append(recErrs, RecordError{Index: i, Error: err.Error()})
			continue
		}
		recs = append(recs, rec)
	}

	return a.process(ctx, src, recs, recErrs, len(body))
}

// process runs the shared ingestion pipeline for an already-mapped batch of
// records: rate limit, quota, sampling, persist, live tail, usage + metrics.
// Both the bulk and OTLP listeners funnel through here.
func (a *app) process(ctx context.Context, src sourcebus.Source, recs []ingest.Record, recErrs []RecordError, bodyLen int) web.Encoder {
	now := time.Now().UTC()

	// Per-source rate limit. Charge one token per record in the batch; shed the
	// whole request with 429 + Retry-After so the agent buffers and retries.
	if ok, retry := a.limiter.AllowN(src.ID, src.RateLimitPerSec, src.RateLimitBurst, len(recs), now); !ok {
		return a.throttled(ctx, retry, "rate limit exceeded")
	}

	// Per-org daily quota. Over quota also sheds with 429 (agents retry next day
	// / after an upgrade); emit a quota_exceeded signal for the tenant.
	if a.usageBus != nil {
		status, err := a.usageBus.CheckQuota(ctx, src.OrgID, now)
		if err != nil {
			a.log.Error(ctx, "ingest: check quota", "orgID", src.OrgID, "err", err)
		} else if status.Exceeded() {
			a.log.Info(ctx, "source.quota_exceeded", "orgID", src.OrgID, "sourceID", src.ID,
				"quota", status.Quota, "used", status.Used)
			return a.throttled(ctx, time.Hour, "daily ingest quota exceeded")
		}
	}

	newLogs := make([]logbus.NewLog, 0, len(recs))
	dropped := 0
	for _, rec := range recs {
		ingest.Normalize(&rec, now, a.redactor)

		// Sampling: keep all WARN/ERROR, sample DEBUG/INFO at the source's rate.
		if !ingest.KeepRecord(rec.Level, src.SampleDebugInfo, rand.Float64()) {
			dropped++
			continue
		}

		newLogs = append(newLogs, recordToNewLog(src, rec))
	}

	accepted := 0
	errored := 0
	if len(newLogs) > 0 {
		logs, err := a.logBus.BulkCreate(ctx, newLogs)
		if err != nil {
			return errs.Errorf(errs.Internal, "bulkcreate: %s", err)
		}
		accepted = len(logs)

		// Counted from what was persisted rather than what was submitted, so the
		// health error rate cannot exceed the events it is a share of.
		for _, l := range logs {
			if l.Level.Equal(logbus.LevelError) {
				errored++
			}
		}

		// Best-effort live tail; never block the write path on subscribers.
		a.hub.BroadcastLogs(logs)
	}

	a.recordUsage(ctx, src, now, accepted, bodyLen, dropped, errored)
	a.touchLastSeen(ctx, src.ID, now)

	metrics.AddIngestAccepted(ctx, accepted)
	metrics.AddIngestRejected(ctx, len(recErrs))
	metrics.AddIngestDropped(ctx, dropped)

	return BulkResponse{Accepted: accepted, Rejected: len(recErrs), Errors: recErrs}
}

// throttled sets a Retry-After header and returns a 429 response.
func (a *app) throttled(ctx context.Context, retry time.Duration, msg string) web.Encoder {
	metrics.AddIngestThrottled(ctx)
	if w := web.GetWriter(ctx); w != nil {
		secs := max(int(retry.Seconds()), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	return errs.New(errs.ResourceExhausted, errors.New(msg))
}

// recordUsage folds this batch's counters into the source's daily tally. It is
// best-effort and runs asynchronously so it never adds latency to the request.
func (a *app) recordUsage(ctx context.Context, src sourcebus.Source, now time.Time, accepted, bytes, dropped, errored int) {
	if a.usageBus == nil {
		return
	}
	u := usagebus.Usage{
		SourceID:     src.ID,
		OrgID:        src.OrgID,
		ProjectID:    src.ProjectID,
		Day:          now,
		EventCount:   int64(accepted),
		ByteCount:    int64(bytes),
		DroppedCount: int64(dropped),
		ErrorCount:   int64(errored),
	}
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := a.usageBus.Record(bg, u); err != nil {
			a.log.Error(bg, "ingest: record usage", "sourceID", src.ID, "err", err)
		}
	}()
}

// bulkToRecord maps a single HTTP-bulk record onto a normalized ingest.Record.
// It validates only what's mandatory (message); everything else is defaulted.
func bulkToRecord(src sourcebus.Source, rec BulkRecord, now time.Time) (ingest.Record, error) {
	if rec.Message == "" {
		return ingest.Record{}, errors.New("message is required")
	}

	level := logbus.LevelInfo
	if rec.Level != "" {
		if lvl, err := logbus.ParseLevel(normalizeLevel(rec.Level)); err == nil {
			level = lvl
		}
	}

	source := rec.Source
	if source == "" {
		source = src.Kind
	}

	attrs := rec.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}

	return ingest.Record{
		Level:     level,
		Message:   rec.Message,
		Source:    source,
		Timestamp: parseTimestamp(rec.Timestamp, now),
		Tags:      rec.Tags,
		Infra: logbus.Infra{
			Host:            rec.Host,
			Container:       rec.Container,
			Pod:             rec.Pod,
			Namespace:       rec.Namespace,
			Cluster:         rec.Cluster,
			Unit:            rec.Unit,
			Facility:        rec.Facility,
			Region:          rec.Region,
			CloudResourceID: rec.CloudResourceID,
		},
		Attributes: attrs,
	}, nil
}

// recordToNewLog stamps a normalized record as an infra log bound to the
// source's project. The target project comes from the source, never from client
// input, so a key can only write into its own tenant.
func recordToNewLog(src sourcebus.Source, rec ingest.Record) logbus.NewLog {
	sourceID := src.ID
	return logbus.NewLog{
		ProjectID:  src.ProjectID,
		Level:      rec.Level,
		Message:    rec.Message,
		Source:     rec.Source,
		Timestamp:  rec.Timestamp,
		Tags:       rec.Tags,
		SourceType: logbus.SourceTypeInfra,
		SourceID:   &sourceID,
		Infra:      rec.Infra,
		Attributes: rec.Attributes,
	}
}

// touchLastSeen updates the source's last_seen_at, throttled per source and run
// asynchronously so it never adds latency to the ingest path.
func (a *app) touchLastSeen(ctx context.Context, id uuid.UUID, now time.Time) {
	a.touchMu.Lock()
	last, ok := a.lastTouch[id]
	if ok && now.Sub(last) < touchInterval {
		a.touchMu.Unlock()
		return
	}
	a.lastTouch[id] = now
	a.touchMu.Unlock()

	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := a.sourceBus.TouchLastSeen(bg, id, now); err != nil {
			a.log.Error(bg, "ingest: touch last_seen", "sourceID", id, "err", err)
		}
	}()
}
