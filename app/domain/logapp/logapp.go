// Package logapp maintains the app layer api for the log domain.
package logapp

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/web"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type app struct {
	logBus logbus.ExtBusiness
	hub    *Hub
}

func newApp(logBus logbus.ExtBusiness, hub *Hub) *app {
	return &app{logBus: logBus, hub: hub}
}

// ingest handles POST /v1/ingest.
// Accepts a single log object or an array.
func (a *app) ingest(ctx context.Context, r *http.Request) web.Encoder {
	var req IngestRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	if len(req) == 0 {
		return errs.New(errs.InvalidArgument, fmt.Errorf("request body must contain at least one entry"))
	}

	newLogs, fieldErrs := toBusNewLogs(req)
	if fieldErrs != nil {
		return fieldErrs.ToError()
	}

	logs, err := a.logBus.BulkCreate(ctx, newLogs)
	if err != nil {
		return errs.Errorf(errs.Internal, "bulkcreate: %s", err)
	}

	ids := make([]string, len(logs))
	entries := make([]LogEntry, len(logs))
	for i, l := range logs {
		ids[i] = l.ID.String()
		entries[i] = toAppLogEntry(l)
	}

	// Broadcast to any connected WebSocket clients grouped by project.
	byProject := make(map[uuid.UUID][]LogEntry)
	for _, e := range entries {
		pid, _ := uuid.Parse(e.PID)
		byProject[pid] = append(byProject[pid], e)
	}
	for pid, es := range byProject {
		a.hub.broadcast(pid, es)
	}

	return IngestResponse{Ingested: len(logs), IDs: ids}
}

// query handles GET /v1/projects/{project_id}/logs.
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	q := r.URL.Query()

	filter := logbus.QueryFilter{ProjectID: projectID}

	if lvlStr := q.Get("level"); lvlStr != "" {
		lvl, err := logbus.ParseLevel(lvlStr)
		if err != nil {
			return errs.New(errs.InvalidArgument, err)
		}
		filter.Level = &lvl
	}

	if search := q.Get("search"); search != "" {
		filter.Search = &search
	}

	if fromStr := q.Get("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return errs.New(errs.InvalidArgument, fmt.Errorf("invalid 'from': %w", err))
		}
		filter.From = &t
	}

	if toStr := q.Get("to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return errs.New(errs.InvalidArgument, fmt.Errorf("invalid 'to': %w", err))
		}
		filter.To = &t
	}

	limit := 100
	if limitStr := q.Get("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, fmt.Errorf("invalid 'limit'"))
		}
		if n > 1000 {
			n = 1000
		}
		limit = n
	}

	cursor := q.Get("cursor")

	result, err := a.logBus.Query(ctx, filter, limit, cursor)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	appLogs := make([]LogEntry, len(result.Logs))
	for i, l := range result.Logs {
		appLogs[i] = toAppLogEntry(l)
	}

	return LogsResponse{
		Logs:       appLogs,
		NextCursor: result.NextCursor,
		Total:      result.Total,
	}
}

// stats handles GET /v1/projects/{project_id}/logs/stats.
func (a *app) stats(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	counts, err := a.logBus.Stats(ctx, projectID)
	if err != nil {
		return errs.Errorf(errs.Internal, "stats: %s", err)
	}

	return StatsResponse(counts)
}

// stream handles GET /v1/projects/{project_id}/logs/stream (WebSocket).
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		http.Error(w, "invalid project_id", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	a.hub.subscribe(projectID, conn)
	defer a.hub.unsubscribe(projectID, conn)

	// Keep alive — read until client disconnects.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
