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
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	log        *logger.Logger
	logBus     logbus.ExtBusiness
	projectBus projectbus.ExtBusiness
	hub        *Hub
	authClient authclient.Authenticator
	upgrader   websocket.Upgrader
}

func newApp(log *logger.Logger, logBus logbus.ExtBusiness, projectBus projectbus.ExtBusiness, hub *Hub, authClient authclient.Authenticator, allowedOrigins []string) *app {
	// Build an origin set for O(1) lookup.
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = struct{}{}
	}

	checkOrigin := func(r *http.Request) bool {
		// If the config says "*" allow everything (dev / wildcard CORS).
		if _, ok := originSet["*"]; ok {
			return true
		}
		_, ok := originSet[r.Header.Get("Origin")]
		return ok
	}

	return &app{
		log:        log,
		logBus:     logBus,
		projectBus: projectBus,
		hub:        hub,
		authClient: authClient,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     checkOrigin,
		},
	}
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

	// Enforce project-level access unless the caller is a SUPER ADMIN.
	claims := mid.GetClaims(ctx)
	isSuperAdmin := false
	for _, r := range claims.Roles {
		if r == role.Admin.String() {
			isSuperAdmin = true
			break
		}
	}

	if !isSuperAdmin {
		userID := mid.GetSubjectID(ctx)
		seen := make(map[uuid.UUID]struct{})
		for _, nl := range newLogs {
			if _, checked := seen[nl.ProjectID]; checked {
				continue
			}
			seen[nl.ProjectID] = struct{}{}

			ok, err := a.projectBus.HasAccess(ctx, userID, nl.ProjectID)
			if err != nil {
				return errs.Errorf(errs.Internal, "hasaccess: projectID[%s]: %s", nl.ProjectID, err)
			}
			if !ok {
				return errs.Errorf(errs.PermissionDenied, "user does not have access to project %s", nl.ProjectID)
			}
		}
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
//
// Authentication note: browsers cannot set custom headers (like Authorization)
// on WebSocket connections. The frontend therefore passes the JWT as a query
// parameter: ?token=<jwt>. We manually validate it here using the same
// authclient that the HTTP middleware uses, reconstructing the expected
// "Bearer <token>" header value from the query param.
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	// ── 1. Authenticate ───────────────────────────────────────────────────
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	authResp, err := a.authClient.Authenticate(r.Context(), "Bearer "+token)
	if err != nil {
		a.log.Info(r.Context(), "stream: authenticate failed", "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// ── 2. Parse project ID ───────────────────────────────────────────────
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		http.Error(w, "invalid project_id", http.StatusBadRequest)
		return
	}

	// ── 2a. Authorize project access ──────────────────────────────────────
	// Super admins have system-wide access; skip the per-project check.
	isSuperAdmin := false
	for _, claimRole := range authResp.Claims.Roles {
		if claimRole == role.Admin.String() {
			isSuperAdmin = true
			break
		}
	}
	if !isSuperAdmin {
		ok, err := a.projectBus.HasAccess(r.Context(), authResp.UserID, projectID)
		if err != nil {
			a.log.Info(r.Context(), "stream: hasaccess error", "userID", authResp.UserID, "projectID", projectID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			a.log.Info(r.Context(), "stream: access denied", "userID", authResp.UserID, "projectID", projectID)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// ── 3. Upgrade to WebSocket ───────────────────────────────────────────
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade writes the error response itself; nothing more to do.
		return
	}
	defer conn.Close()

	// ── 4. Register with the hub ──────────────────────────────────────────
	a.hub.subscribe(projectID, conn)
	defer a.hub.unsubscribe(projectID, conn)

	// ── 5. Keep-alive read loop ───────────────────────────────────────────
	// Block until the client disconnects (or sends anything, which we ignore).
	// Setting a read deadline / pong handler would be the production hardening
	// step here, but is out of scope for this fix.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
