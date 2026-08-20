package logapp

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

// MaxOrgLogProjects bounds the per-project fan-out of an org-wide read. Each
// project costs one index scan, so the cost is linear in this number.
const MaxOrgLogProjects = 50

// queryOrg reads logs across several projects in one organization.
//
// GET /v1/orgs/{org_id}/logs?projectId=…&<same filters as the per-project route>
//
// projectId is repeatable and comma-separated. Omitting it reads every project
// the caller can see, which is the whole point: a cross-project view previously
// needed one request per project.
func (a *app) queryOrg(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	visible, errResp := a.visibleProjects(ctx, orgID)
	if errResp != nil {
		return errResp
	}

	selected, errResp := selectProjects(r, visible)
	if errResp != nil {
		return errResp
	}

	filter, limit, cursor, errResp := parseLogQuery(r)
	if errResp != nil {
		return errResp
	}

	// A member who can see no project in this org gets an empty page rather than
	// a denial: they are entitled to ask, there is simply nothing to show.
	if len(selected) == 0 {
		return LogsResponse{Logs: []LogEntry{}, TotalIsExact: true}
	}

	filter.ProjectIDs = selected

	return a.runLogQuery(ctx, filter, limit, cursor)
}

// visibleProjects returns the project ids in orgID the caller may read. A global
// SUPER ADMIN sees all of them, matching the bypass in AuthorizeProjectAccess;
// everyone else is resolved from their membership and per-project grants.
func (a *app) visibleProjects(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, web.Encoder) {
	claims := mid.GetClaims(ctx)
	for _, r := range claims.Roles {
		if r == role.Admin.String() {
			projects, err := a.projectBus.QueryByOrg(ctx, orgID)
			if err != nil {
				return nil, errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
			}
			return projectIDs(projects), nil
		}
	}

	userID, err := mid.GetUserID(ctx)
	if err != nil {
		return nil, errs.New(errs.Unauthenticated, err)
	}

	projects, err := a.projectBus.QueryVisibleByOrg(ctx, orgID, userID)
	if err != nil {
		return nil, errs.Errorf(errs.Internal, "queryvisiblebyorg: orgID[%s]: %s", orgID, err)
	}

	return projectIDs(projects), nil
}

// selectProjects narrows the visible set by the requested projectId values.
//
// A requested project the caller cannot see is reported as not found rather than
// silently dropped: quietly returning another project's results, or an empty
// page, would both be worse than saying so. It is not distinguishable from a
// project that does not exist, which is deliberate.
func selectProjects(r *http.Request, visible []uuid.UUID) ([]uuid.UUID, web.Encoder) {
	requested := csvValues(r.URL.Query()["projectId"])

	if len(requested) == 0 {
		if len(visible) > MaxOrgLogProjects {
			return nil, errs.Errorf(errs.InvalidArgument,
				"this organization has more than %d visible projects; pass projectId to choose up to %d",
				MaxOrgLogProjects, MaxOrgLogProjects)
		}
		return visible, nil
	}

	if len(requested) > MaxOrgLogProjects {
		return nil, errs.Errorf(errs.InvalidArgument, "at most %d projectId values may be given", MaxOrgLogProjects)
	}

	allowed := make(map[uuid.UUID]struct{}, len(visible))
	for _, id := range visible {
		allowed[id] = struct{}{}
	}

	seen := make(map[uuid.UUID]struct{}, len(requested))
	out := make([]uuid.UUID, 0, len(requested))

	for _, raw := range requested {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, errs.Errorf(errs.InvalidArgument, "invalid projectId %q", raw)
		}
		if _, ok := allowed[id]; !ok {
			return nil, errs.Errorf(errs.NotFound, "project %s not found in this organization", id)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	return out, nil
}

func projectIDs(projects []projectbus.Project) []uuid.UUID {
	out := make([]uuid.UUID, len(projects))
	for i, p := range projects {
		out[i] = p.ID
	}
	return out
}
