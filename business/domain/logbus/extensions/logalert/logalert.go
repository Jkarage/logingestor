// Package logalert provides a logbus extension that fires integration alerts
// after logs are persisted.
package logalert

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// queueSize bounds how many persisted batches may wait for alert dispatch.
// When the queue is full new batches are dropped: alerts are best-effort and
// a slow provider must never translate into unbounded goroutine or memory
// growth on the ingest path.
const queueSize = 128

// dispatchTimeout bounds the total time spent dispatching one batch (project
// lookups, rule queries, and provider calls), so a hung provider or database
// cannot stall the worker forever.
const dispatchTimeout = 30 * time.Second

// Extension wraps logbus with alert-firing logic. Persisted batches are handed
// to a single background worker through a bounded queue, so ingest never
// blocks on alert delivery and dispatch work cannot pile up without bound.
type Extension struct {
	bus        logbus.ExtBusiness
	log        *logger.Logger
	projectBus projectbus.ExtBusiness
	alertBus   *integrationbus.Business
	queue      chan []logbus.Log
}

// NewExtension returns a logbus.Extension that fires integration alerts after each BulkCreate.
func NewExtension(log *logger.Logger, projectBus projectbus.ExtBusiness, alertBus *integrationbus.Business) logbus.Extension {
	return func(bus logbus.ExtBusiness) logbus.ExtBusiness {
		ext := &Extension{
			bus:        bus,
			log:        log,
			projectBus: projectBus,
			alertBus:   alertBus,
			queue:      make(chan []logbus.Log, queueSize),
		}
		go ext.worker()
		return ext
	}
}

// BulkCreate persists logs then queues them for asynchronous alert dispatch.
func (ext *Extension) BulkCreate(ctx context.Context, entries []logbus.NewLog) ([]logbus.Log, error) {
	logs, err := ext.bus.BulkCreate(ctx, entries)
	if err != nil {
		return nil, err
	}

	if len(logs) > 0 {
		select {
		case ext.queue <- logs:
		default:
			ext.log.Error(ctx, "logalert: dispatch queue full, dropping batch", "count", len(logs))
		}
	}

	return logs, nil
}

// worker drains the queue for the life of the process, giving each batch a
// bounded context.
func (ext *Extension) worker() {
	for logs := range ext.queue {
		ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
		ext.dispatch(ctx, logs)
		cancel()
	}
}

// dispatch groups the batch's logs by project and fires alerts once per
// project, so rule matching costs one query per project rather than one per log.
func (ext *Extension) dispatch(ctx context.Context, logs []logbus.Log) {
	names := make(map[uuid.UUID]string)
	failed := make(map[uuid.UUID]bool)
	byProject := make(map[uuid.UUID][]integrationbus.AlertPayload)

	for _, l := range logs {
		if failed[l.ProjectID] {
			continue
		}

		name, ok := names[l.ProjectID]
		if !ok {
			project, err := ext.projectBus.QueryByID(ctx, l.ProjectID)
			if err != nil {
				ext.log.Error(ctx, "logalert: lookup project", "projectID", l.ProjectID, "err", err)
				failed[l.ProjectID] = true
				continue
			}
			name = project.Name
			names[l.ProjectID] = name
		}

		byProject[l.ProjectID] = append(byProject[l.ProjectID], integrationbus.AlertPayload{
			ProjectName: name,
			Level:       l.Level.String(),
			Message:     l.Message,
			Source:      l.Source,
			LogID:       l.ID.String(),
			Timestamp:   l.Timestamp,
		})
	}

	for pid, payloads := range byProject {
		if err := ext.alertBus.FireAlerts(ctx, pid, payloads); err != nil {
			ext.log.Error(ctx, "logalert: fire alerts", "projectID", pid, "err", err)
		}
	}
}

func (ext *Extension) QueryByID(ctx context.Context, id uuid.UUID) (logbus.Log, error) {
	return ext.bus.QueryByID(ctx, id)
}

func (ext *Extension) Query(ctx context.Context, filter logbus.QueryFilter, limit int, cursor string) (logbus.QueryResult, error) {
	return ext.bus.Query(ctx, filter, limit, cursor)
}

func (ext *Extension) Stats(ctx context.Context, projectID uuid.UUID, sourceType *string) (map[string]int, error) {
	return ext.bus.Stats(ctx, projectID, sourceType)
}

func (ext *Extension) Timeseries(ctx context.Context, req logbus.TimeseriesRequest) ([]logbus.Bucket, error) {
	return ext.bus.Timeseries(ctx, req)
}

func (ext *Extension) Aggregate(ctx context.Context, req logbus.AggregateRequest) ([]logbus.Group, error) {
	return ext.bus.Aggregate(ctx, req)
}
