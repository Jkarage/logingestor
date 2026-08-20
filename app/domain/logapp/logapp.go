// Package logapp maintains the app layer api for the log domain.
package logapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/analyzebus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	log        *logger.Logger
	logBus     logbus.ExtBusiness
	projectBus projectbus.ExtBusiness
	analyzeBus *analyzebus.Business
	hub        *Hub
	authClient authclient.Authenticator
	upgrader   websocket.Upgrader
	tickets    *ticketStore

	// usageBus is optional; nil disables app-log metering and quota enforcement.
	usageBus usagebus.ExtBusiness
}

func newApp(log *logger.Logger, logBus logbus.ExtBusiness, projectBus projectbus.ExtBusiness, analyzeBus *analyzebus.Business, hub *Hub, authClient authclient.Authenticator, allowedOrigins []string, usageBus usagebus.ExtBusiness) *app {
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
		analyzeBus: analyzeBus,
		hub:        hub,
		authClient: authClient,
		tickets:    newTicketStore(),
		usageBus:   usageBus,
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

	// Resolve each distinct project once: its owning org drives both the
	// suspension check below and the per-org quota check further down.
	//
	// A suspended organization accepts no new logs, regardless of the caller's
	// role — suspension is a billing/lifecycle state, not an authorization one.
	orgByProject := make(map[uuid.UUID]uuid.UUID)
	for _, nl := range newLogs {
		if _, done := orgByProject[nl.ProjectID]; done {
			continue
		}

		project, err := a.projectBus.QueryByID(ctx, nl.ProjectID)
		if err != nil {
			if errors.Is(err, projectbus.ErrNotFound) {
				return errs.Errorf(errs.NotFound, "project %s not found", nl.ProjectID)
			}
			return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", nl.ProjectID, err)
		}
		orgByProject[nl.ProjectID] = project.OrgID

		enabled, err := a.projectBus.OrgEnabled(ctx, nl.ProjectID)
		if err != nil {
			if errors.Is(err, projectbus.ErrNotFound) {
				return errs.Errorf(errs.NotFound, "project %s not found", nl.ProjectID)
			}
			return errs.Errorf(errs.Internal, "orgenabled: projectID[%s]: %s", nl.ProjectID, err)
		}
		if !enabled {
			return errs.Errorf(errs.OrgSuspended, "organization for project %s is suspended", nl.ProjectID)
		}
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

	// Per-org daily app-log quota, checked once per distinct org in the batch.
	if a.usageBus != nil {
		checked := make(map[uuid.UUID]struct{}, len(orgByProject))
		for _, orgID := range orgByProject {
			if _, done := checked[orgID]; done {
				continue
			}
			checked[orgID] = struct{}{}

			status, err := a.usageBus.CheckAppQuota(ctx, orgID, time.Now().UTC())
			if err != nil {
				// Metering must not take ingestion down; log and let the write through.
				a.log.Error(ctx, "ingest: check app quota", "orgID", orgID, "err", err)
				continue
			}
			if status.Exceeded() {
				a.log.Info(ctx, "ingest: app quota exceeded", "orgID", orgID,
					"quota", status.Quota, "used", status.Used)
				return errs.Errorf(errs.TooManyRequests,
					"daily app-log ingest quota exceeded (%d events)", status.Quota)
			}
		}
	}

	logs, err := a.logBus.BulkCreate(ctx, newLogs)
	if err != nil {
		return errs.Errorf(errs.Internal, "bulkcreate: %s", err)
	}

	a.recordAppUsage(ctx, logs, orgByProject)

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

	filter, limit, cursor, errResp := parseLogQuery(r)
	if errResp != nil {
		return errResp
	}
	filter.ProjectID = projectID

	return a.runLogQuery(ctx, filter, limit, cursor)
}

// runLogQuery executes a prepared filter and renders the page. Shared by the
// per-project and org-wide endpoints so both return an identical shape.
func (a *app) runLogQuery(ctx context.Context, filter logbus.QueryFilter, limit int, cursor string) web.Encoder {
	result, err := a.logBus.Query(ctx, filter, limit, cursor)
	if err != nil {
		if errors.Is(err, logbus.ErrWindowTooWide) {
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	appLogs := make([]LogEntry, len(result.Logs))
	for i, l := range result.Logs {
		appLogs[i] = toAppLogEntry(l)
	}

	return LogsResponse{
		Logs:         appLogs,
		NextCursor:   result.NextCursor,
		Total:        result.Total,
		TotalIsExact: result.TotalIsExact,
	}
}

// parseLogQuery reads every filter, paging and total option from the query
// string. It deliberately does not touch project scoping — each endpoint sets
// that itself — so the two share one contract for everything else.
func parseLogQuery(r *http.Request) (logbus.QueryFilter, int, string, web.Encoder) {
	var filter logbus.QueryFilter

	q := r.URL.Query()

	if st, err := parseSourceType(q.Get("source_type")); err != nil {
		return filter, 0, "", errs.New(errs.InvalidArgument, err)
	} else if st != nil {
		filter.SourceType = st
	}

	// level is repeatable and also accepts a comma-separated list, so
	// ?level=WARN&level=ERROR and ?level=WARN,ERROR are equivalent.
	for _, lvlStr := range csvValues(q["level"]) {
		lvl, err := logbus.ParseLevel(lvlStr)
		if err != nil {
			return filter, 0, "", errs.New(errs.InvalidArgument, err)
		}
		filter.Levels = append(filter.Levels, lvl)
	}

	// q is the documented name for free-text search; search is the original and
	// stays accepted so existing callers keep working.
	if search := firstNonEmpty(q.Get("q"), q.Get("search")); search != "" {
		filter.Search = &search
	}

	if source := strings.TrimSpace(q.Get("source")); source != "" {
		filter.Source = &source
	}

	// tag is repeatable and comma-separated; every tag given must be present.
	filter.Tags = csvValues(q["tag"])

	// meta.<field>=value filters on the structured payload, e.g.
	// ?meta.orderId=123&meta.traceId=abc. Every pair must match.
	filter.Meta = metaFilters(q)

	for _, f := range []struct {
		name string
		dst  **time.Time
	}{{"from", &filter.From}, {"to", &filter.To}} {
		v := q.Get(f.name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, 0, "", errs.Errorf(errs.InvalidArgument, "invalid '%s': want RFC3339", f.name)
		}
		*f.dst = &t
	}

	limit := 100
	if limitStr := q.Get("limit"); limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n <= 0 {
			return filter, 0, "", errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		if n > 1000 {
			n = 1000
		}
		limit = n
	}

	// total=bounded (default) keeps the count index-only and capped;
	// total=exact opts into a true count(1), which on a project holding tens of
	// millions of rows is a multi-second sequential scan; total=none skips it.
	switch q.Get("total") {
	case "", "bounded":
		filter.TotalMode = logbus.TotalBounded
	case "none":
		filter.TotalMode = logbus.TotalNone
	case "exact":
		filter.TotalMode = logbus.TotalExact
	default:
		return filter, 0, "", errs.New(errs.InvalidArgument, errors.New("invalid 'total': want bounded, exact, or none"))
	}

	return filter, limit, q.Get("cursor"), nil
}

// parseSourceType validates the optional source_type query param. An empty
// value means "all" (nil filter); "app"/"infra" scope the query.
func parseSourceType(s string) (*string, error) {
	switch s {
	case "":
		return nil, nil
	case logbus.SourceTypeApp, logbus.SourceTypeInfra:
		v := s
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid source_type %q (want app|infra)", s)
	}
}

// stats handles GET /v1/projects/{project_id}/logs/stats.
func (a *app) stats(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	st, err := parseSourceType(r.URL.Query().Get("source_type"))
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	counts, err := a.logBus.Stats(ctx, projectID, st)
	if err != nil {
		return errs.Errorf(errs.Internal, "stats: %s", err)
	}

	return StatsResponse(counts)
}

// analyze handles POST /v1/projects/{project_id}/logs/{log_id}/analyze.
func (a *app) analyze(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	logID, err := uuid.Parse(web.Param(r, "log_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	l, err := a.logBus.QueryByID(ctx, logID)
	if err != nil {
		if errors.Is(err, logbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: %s", err)
	}

	if l.ProjectID != projectID {
		return errs.New(errs.NotFound, logbus.ErrNotFound)
	}

	analysis, err := a.analyzeBus.Analyze(ctx, l)
	if err != nil {
		return errs.Errorf(errs.Internal, "analyze: %s", err)
	}

	return AnalyzeResponse(analysis)
}

// stream handles GET /v1/projects/{project_id}/logs/stream (WebSocket).
//
// Authentication note: browsers cannot set custom headers (like Authorization)
// on WebSocket connections. The frontend therefore passes the JWT as a query
// parameter: ?token=<jwt>. We manually validate it here using the same
// authclient that the HTTP middleware uses, reconstructing the expected
// "Bearer <token>" header value from the query param.
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	// ── 1. Parse project ID ───────────────────────────────────────────────
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		streamError(w, http.StatusBadRequest, "invalid_argument", "invalid project_id")
		return
	}

	// ── 2. Authenticate ───────────────────────────────────────────────────
	// Preferred: a single-use ticket from POST .../logs/stream-ticket. The
	// ticket already encodes an authorized (user, project) pair, so redeeming it
	// is both authentication and authorization.
	//
	// Deprecated fallback: ?token=<jwt>. The JWT leaks into proxy/LB access logs
	// from the query string, which is why tickets exist. Delete this branch once
	// the frontend has moved over.
	if raw := r.URL.Query().Get("ticket"); raw != "" {
		userID, err := a.tickets.consume(raw, projectID, time.Now())
		if err != nil {
			status, code := http.StatusUnauthorized, "unauthenticated"
			if errors.Is(err, errTicketProject) {
				status, code = http.StatusForbidden, "permission_denied"
			}
			a.log.Info(r.Context(), "stream: ticket rejected", "projectID", projectID, "err", err)
			streamError(w, status, code, err.Error())
			return
		}
		a.log.Info(r.Context(), "stream: ticket accepted", "userID", userID, "projectID", projectID)
	} else {
		token := r.URL.Query().Get("token")
		if token == "" {
			streamError(w, http.StatusUnauthorized, "unauthenticated", "missing stream ticket")
			return
		}

		a.log.Info(r.Context(), "stream: deprecated ?token= auth used", "projectID", projectID)

		authResp, err := a.authClient.Authenticate(r.Context(), "Bearer "+token)
		if err != nil {
			a.log.Info(r.Context(), "stream: authenticate failed", "err", err)
			streamError(w, http.StatusUnauthorized, "unauthenticated", "unauthorized")
			return
		}

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
				streamError(w, http.StatusInternalServerError, "internal", "internal error")
				return
			}
			if !ok {
				a.log.Info(r.Context(), "stream: access denied", "userID", authResp.UserID, "projectID", projectID)
				streamError(w, http.StatusForbidden, "permission_denied", "forbidden")
				return
			}
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

// recordAppUsage folds the accepted batch into the per-project daily counters
// that back GET /v1/orgs/{org_id}/usage and the app-log quota.
//
// Byte counts are an approximation: the message, source and the encoded
// structured fields, not the raw HTTP payload, because one request may carry
// entries for several projects and the wire bytes cannot be attributed exactly.
//
// Recording happens off the request path. Losing a counter update is preferable
// to failing an ingest that has already been persisted.
func (a *app) recordAppUsage(ctx context.Context, logs []logbus.Log, orgByProject map[uuid.UUID]uuid.UUID) {
	if a.usageBus == nil || len(logs) == 0 {
		return
	}

	type tally struct {
		events int64
		bytes  int64
	}

	byProject := make(map[uuid.UUID]*tally)

	for _, l := range logs {
		t, ok := byProject[l.ProjectID]
		if !ok {
			t = &tally{}
			byProject[l.ProjectID] = t
		}

		t.events++
		t.bytes += int64(len(l.Message) + len(l.Source))

		for _, m := range []map[string]any{l.Meta, l.Attributes} {
			if len(m) == 0 {
				continue
			}
			if encoded, err := json.Marshal(m); err == nil {
				t.bytes += int64(len(encoded))
			}
		}
	}

	day := time.Now().UTC()

	deltas := make([]usagebus.AppUsage, 0, len(byProject))
	for projectID, t := range byProject {
		deltas = append(deltas, usagebus.AppUsage{
			ProjectID:  projectID,
			OrgID:      orgByProject[projectID],
			Day:        day,
			EventCount: t.events,
			ByteCount:  t.bytes,
		})
	}

	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		for _, d := range deltas {
			if err := a.usageBus.RecordApp(bg, d); err != nil {
				a.log.Error(bg, "ingest: record app usage", "projectID", d.ProjectID, "err", err)
			}
		}
	}()
}

// csvValues flattens repeated query parameters that may also carry
// comma-separated lists, trimming blanks. ?tag=a&tag=b,c yields [a b c].
func csvValues(raw []string) []string {
	var out []string

	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}

	return out
}

// firstNonEmpty returns the first non-blank value, so a parameter can have an
// alias without the caller needing to know which one won.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// metaKeyPattern bounds a meta field name. The value is always bound as a JSON
// parameter, so this guards against nonsense keys rather than injection.
var metaKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,128}$`)

// metaFilters collects ?meta.<field>=value pairs from the query string. Keys
// that do not look like field names are ignored rather than erroring, so an
// unrelated future parameter beginning "meta." cannot break existing callers.
func metaFilters(q url.Values) map[string]string {
	var out map[string]string

	for key, vals := range q {
		field, ok := strings.CutPrefix(key, "meta.")
		if !ok || len(vals) == 0 {
			continue
		}

		v := strings.TrimSpace(vals[0])
		if v == "" || !metaKeyPattern.MatchString(field) {
			continue
		}

		if out == nil {
			out = make(map[string]string)
		}
		out[field] = v
	}

	return out
}
