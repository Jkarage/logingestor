package logapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// The public query API is the same read path as the app's, authenticated by a
// read-only API key instead of a session. Scope comes from the key — its org,
// and its project if it is pinned to one — never from the request, so a key
// cannot be pointed at another tenant by editing a query string.

// QueryProject is a project as the query API reports it, so a script can
// discover the ids it needs to filter by.
type QueryProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// QueryProjects is the list response shape.
type QueryProjects struct {
	Projects []QueryProject `json:"projects"`
}

// Encode implements the encoder interface.
func (app QueryProjects) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// keyScope resolves the projects an API key may read.
//
// A pinned key reads exactly one project. An org-wide key reads every project in
// its org — including ones created after the key was issued, which is the point
// of an org-wide key.
func (a *app) keyScope(ctx context.Context) ([]uuid.UUID, uuid.UUID, web.Encoder) {
	key, err := mid.GetAPIKey(ctx)
	if err != nil {
		return nil, uuid.UUID{}, errs.New(errs.Unauthenticated, err)
	}

	if key.ProjectID != nil {
		return []uuid.UUID{*key.ProjectID}, key.OrgID, nil
	}

	projects, err := a.projectBus.QueryByOrg(ctx, key.OrgID)
	if err != nil {
		return nil, uuid.UUID{}, errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", key.OrgID, err)
	}

	return projectIDs(projects), key.OrgID, nil
}

// queryAPIProjects handles GET /v1/query/projects.
func (a *app) queryAPIProjects(ctx context.Context, r *http.Request) web.Encoder {
	key, err := mid.GetAPIKey(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	projects, err := a.projectBus.QueryByOrg(ctx, key.OrgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", key.OrgID, err)
	}

	out := make([]QueryProject, 0, len(projects))
	for _, p := range projects {
		// A pinned key reports only its own project, so its output matches what
		// it can actually query.
		if key.ProjectID != nil && p.ID != *key.ProjectID {
			continue
		}
		out = append(out, QueryProject{ID: p.ID.String(), Name: p.Name})
	}

	return QueryProjects{Projects: out}
}

// queryAPILogs handles GET /v1/query/logs.
func (a *app) queryAPILogs(ctx context.Context, r *http.Request) web.Encoder {
	filter, limit, cursor, errResp := a.apiFilter(ctx, r)
	if errResp != nil {
		return errResp
	}

	return a.runLogQuery(ctx, filter, limit, cursor)
}

// exportAPILogs handles GET /v1/query/logs/export.
func (a *app) exportAPILogs(ctx context.Context, r *http.Request) web.Encoder {
	filter, _, _, errResp := a.apiFilter(ctx, r)
	if errResp != nil {
		return errResp
	}

	key, err := mid.GetAPIKey(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	if len(filter.ProjectIDs) == 0 && filter.ProjectID == uuid.Nil {
		return a.streamEmpty(ctx, r, "logs-"+key.OrgID.String())
	}

	return a.streamExport(ctx, r, filter, "logs-"+key.OrgID.String())
}

// apiFilter builds a query filter scoped to the key, honouring an optional
// projectId narrowing within that scope.
func (a *app) apiFilter(ctx context.Context, r *http.Request) (logbus.QueryFilter, int, string, web.Encoder) {
	scope, _, errResp := a.keyScope(ctx)
	if errResp != nil {
		return logbus.QueryFilter{}, 0, "", errResp
	}

	filter, limit, cursor, errResp := parseLogQuery(r)
	if errResp != nil {
		return logbus.QueryFilter{}, 0, "", errResp
	}

	selected, errResp := selectProjects(r, scope)
	if errResp != nil {
		return logbus.QueryFilter{}, 0, "", errResp
	}

	switch len(selected) {
	case 0:
		// Nothing in scope. Handled by the caller as an empty result.
	case 1:
		// One project takes the single-project plan, which is the cheaper one.
		filter.ProjectID = selected[0]
	default:
		filter.ProjectIDs = selected
	}

	return filter, limit, cursor, nil
}

// queryAPILogByID handles GET /v1/query/logs/{log_id}.
func (a *app) queryAPILogByID(ctx context.Context, r *http.Request) web.Encoder {
	scope, _, errResp := a.keyScope(ctx)
	if errResp != nil {
		return errResp
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

	// In scope or it does not exist, as far as this key is concerned.
	for _, id := range scope {
		if l.ProjectID == id {
			return toAppLogEntry(l)
		}
	}

	return errs.New(errs.NotFound, logbus.ErrNotFound)
}

// queryAPIStats handles GET /v1/query/stats — per-level counts for one project.
func (a *app) queryAPIStats(ctx context.Context, r *http.Request) web.Encoder {
	scope, _, errResp := a.keyScope(ctx)
	if errResp != nil {
		return errResp
	}

	selected, errResp := selectProjects(r, scope)
	if errResp != nil {
		return errResp
	}

	if len(selected) != 1 {
		return errs.New(errs.InvalidArgument, errors.New("stats needs exactly one projectId"))
	}

	st, err := parseSourceType(r.URL.Query().Get("source_type"))
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	counts, err := a.logBus.Stats(ctx, selected[0], st)
	if err != nil {
		return errs.Errorf(errs.Internal, "stats: %s", err)
	}

	return StatsResponse(counts)
}
