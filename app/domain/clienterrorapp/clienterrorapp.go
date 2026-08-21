package clienterrorapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	log            *logger.Logger
	clientErrorBus *clienterrorbus.Business
	orgBus         orgbus.ExtBusiness
	projectBus     projectbus.ExtBusiness

	// authClient resolves an optional token on the public ingest route. It is not
	// middleware there, because middleware would refuse the anonymous reports
	// that route exists to accept.
	authClient authclient.Authenticator

	allowedOrigins []string
	limiter        *ingestLimiter
}

func newApp(cfg Config) *app {
	origins := cfg.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	return &app{
		log:            cfg.Log,
		clientErrorBus: cfg.ClientErrorBus,
		orgBus:         cfg.OrgBus,
		projectBus:     cfg.ProjectBus,
		authClient:     cfg.AuthClient,
		allowedOrigins: origins,
		limiter:        newIngestLimiter(),
	}
}

// windowFor reads a ?range= of the shape 24h/7d/30d, defaulting to 24 hours.
func windowFor(r *http.Request) (from, to time.Time, err error) {
	to = time.Now().UTC()

	switch r.URL.Query().Get("range") {
	case "", "24h":
		return to.Add(-24 * time.Hour), to, nil
	case "1h":
		return to.Add(-time.Hour), to, nil
	case "7d":
		return to.Add(-7 * 24 * time.Hour), to, nil
	case "30d":
		return to.Add(-30 * 24 * time.Hour), to, nil
	default:
		return time.Time{}, time.Time{}, errors.New("invalid 'range': want 1h, 24h, 7d or 30d")
	}
}

// scope decides which issues a request may read.
//
// An org admin reads their own org. A super admin may additionally read every
// org at once, and the anonymous bucket — which is where the pre-login errors
// live, so somebody has to be able to see them.
type scope struct {
	orgID     *uuid.UUID
	projectID *uuid.UUID
	allOrgs   bool
}

// orgScope resolves the org named in the path, plus an optional ?projectId=
// narrowing within it.
//
// The project is not verified against the org here: the query filters on both,
// so a project from another org simply matches nothing. That is the same answer
// a non-existent project gives, which is what we want it to be.
func orgScope(r *http.Request) (scope, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return scope{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sc := scope{orgID: &orgID}

	if v := r.URL.Query().Get("projectId"); v != "" {
		projectID, err := uuid.Parse(v)
		if err != nil {
			return scope{}, errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
		}
		sc.projectID = &projectID
	}

	return sc, nil
}

// globalScope is the super-admin view: every org plus anonymous reports.
func globalScope(ctx context.Context) (scope, web.Encoder) {
	for _, r := range mid.GetClaims(ctx).Roles {
		if r == role.Admin.String() {
			return scope{allOrgs: true}, nil
		}
	}

	return scope{}, errs.New(errs.PermissionDenied, errors.New("only a super admin may read across organizations"))
}

// queryIssues handles GET /v1/orgs/{org_id}/client-errors/issues.
func (a *app) queryIssues(ctx context.Context, r *http.Request) web.Encoder {
	sc, errResp := orgScope(r)
	if errResp != nil {
		return errResp
	}

	return a.listIssues(ctx, r, sc)
}

// queryIssuesGlobal handles GET /v1/client-errors/issues.
func (a *app) queryIssuesGlobal(ctx context.Context, r *http.Request) web.Encoder {
	sc, errResp := globalScope(ctx)
	if errResp != nil {
		return errResp
	}

	return a.listIssues(ctx, r, sc)
}

func (a *app) listIssues(ctx context.Context, r *http.Request, sc scope) web.Encoder {
	q := r.URL.Query()

	filter := clienterrorbus.IssueFilter{
		OrgID:     sc.orgID,
		ProjectID: sc.projectID,
		AllOrgs:   sc.allOrgs,
		Release:   q.Get("release"),
		Sort:      q.Get("sort"),
		Cursor:    q.Get("cursor"),
	}

	if status := q.Get("status"); status != "" {
		s, err := clienterrorbus.ParseStatus(status)
		if err != nil {
			return errs.New(errs.InvalidArgument, err)
		}
		filter.Status = s
	}

	// The range bounds by last-seen, so "24h" means "issues active today"
	// rather than "issues created today".
	from, _, err := windowFor(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}
	filter.Since = &from

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		filter.Limit = n
	}

	issues, next, err := a.clientErrorBus.QueryIssues(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryissues: %s", err)
	}

	out := Issues{Issues: make([]Issue, len(issues))}
	for i, is := range issues {
		out.Issues[i] = toAppIssue(is)
	}
	if next != "" {
		out.NextCursor = &next
	}

	return out
}

// queryIssue handles GET /v1/orgs/{org_id}/client-errors/issues/{issue_id}.
func (a *app) queryIssue(ctx context.Context, r *http.Request) web.Encoder {
	issue, errResp := a.loadIssue(ctx, r, false)
	if errResp != nil {
		return errResp
	}

	events, err := a.clientErrorBus.QueryIssueEvents(ctx, issue.ID, 20)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryissueevents: %s", err)
	}

	from, to, err := windowFor(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// The bucket width follows the window so a sparkline always has a usable
	// number of points rather than 720 of them.
	interval := time.Hour
	if to.Sub(from) > 7*24*time.Hour {
		interval = 24 * time.Hour
	}

	series, err := a.clientErrorBus.QueryIssueSeries(ctx, issue.ID, from, to, interval)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryissueseries: %s", err)
	}

	detail := IssueDetail{
		Issue:    toAppIssue(issue),
		Events:   make([]Event, len(events)),
		Series:   make([]Point, len(series)),
		Interval: interval.String(),
	}
	for i, e := range events {
		detail.Events[i] = toAppEvent(e)
	}
	if len(detail.Events) > 0 {
		// The newest event, called out so the view does not have to know that the
		// list is ordered.
		latest := detail.Events[0]
		detail.LatestEvent = &latest
	}
	for i, b := range series {
		detail.Series[i] = Point{TS: b.TS.Format(time.RFC3339), Count: b.Count}
	}

	return detail
}

// updateIssue handles PATCH /v1/orgs/{org_id}/client-errors/issues/{issue_id}.
func (a *app) updateIssue(ctx context.Context, r *http.Request) web.Encoder {
	issue, errResp := a.loadIssue(ctx, r, false)
	if errResp != nil {
		return errResp
	}

	var body UpdateIssueRequest
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	ui := clienterrorbus.UpdateIssue{Status: body.Status}

	// Absent leaves the assignee alone; explicit null clears it.
	if len(body.AssigneeID) > 0 {
		var raw *string
		if err := json.Unmarshal(body.AssigneeID, &raw); err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid assigneeId"))
		}

		var assignee *uuid.UUID
		if raw != nil && *raw != "" {
			id, err := uuid.Parse(*raw)
			if err != nil {
				return errs.New(errs.InvalidArgument, errors.New("invalid assigneeId"))
			}
			assignee = &id
		}
		ui.AssigneeID = &assignee
	}

	updated, err := a.clientErrorBus.UpdateIssue(ctx, issue, ui)
	if err != nil {
		if errors.Is(err, clienterrorbus.ErrInvalidStatus) {
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "updateissue: %s", err)
	}

	return toAppIssue(updated)
}

// queryStats handles GET /v1/orgs/{org_id}/client-errors/stats.
func (a *app) queryStats(ctx context.Context, r *http.Request) web.Encoder {
	sc, errResp := orgScope(r)
	if errResp != nil {
		return errResp
	}

	return a.statsFor(ctx, r, sc)
}

// queryStatsGlobal handles GET /v1/client-errors/stats.
func (a *app) queryStatsGlobal(ctx context.Context, r *http.Request) web.Encoder {
	sc, errResp := globalScope(ctx)
	if errResp != nil {
		return errResp
	}

	return a.statsFor(ctx, r, sc)
}

func (a *app) statsFor(ctx context.Context, r *http.Request, sc scope) web.Encoder {
	from, to, err := windowFor(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	s, err := a.clientErrorBus.QueryStats(ctx, sc.orgID, sc.projectID, sc.allOrgs, from, to)
	if err != nil {
		return errs.Errorf(errs.Internal, "querystats: %s", err)
	}

	return Stats{
		From:          s.From.Format(time.RFC3339),
		To:            s.To.Format(time.RFC3339),
		Events:        s.Events,
		Issues:        s.Issues,
		NewIssues:     s.NewIssues,
		Unresolved:    s.Unresolved,
		AffectedUsers: s.AffectedUsers,
	}
}

// purge handles DELETE /v1/orgs/{org_id}/client-errors.
//
// This is the deletion request path. It is org admin rather than owner because
// it destroys diagnostic data, not customer data — and an admin who wants their
// error reports gone should not have to ask us.
func (a *app) purge(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	n, err := a.clientErrorBus.PurgeOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "purgeorg: %s", err)
	}

	return Purged{Purged: n}
}

// loadIssue fetches the issue named in the path and confirms it belongs to the
// scope the caller asked about.
//
// An issue from another org reads as missing rather than forbidden: the id is
// otherwise a way to learn that a given error exists in somebody else's tenant.
func (a *app) loadIssue(ctx context.Context, r *http.Request, global bool) (clienterrorbus.Issue, web.Encoder) {
	var (
		sc      scope
		errResp web.Encoder
	)

	if global {
		sc, errResp = globalScope(ctx)
	} else {
		sc, errResp = orgScope(r)
	}
	if errResp != nil {
		return clienterrorbus.Issue{}, errResp
	}

	issueID, err := uuid.Parse(web.Param(r, "issue_id"))
	if err != nil {
		return clienterrorbus.Issue{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	issue, err := a.clientErrorBus.QueryIssueByID(ctx, issueID)
	if err != nil {
		if errors.Is(err, clienterrorbus.ErrNotFound) {
			return clienterrorbus.Issue{}, errs.New(errs.NotFound, clienterrorbus.ErrNotFound)
		}
		return clienterrorbus.Issue{}, errs.Errorf(errs.Internal, "queryissuebyid: %s", err)
	}

	if !sc.allOrgs {
		switch {
		case sc.orgID == nil && issue.OrgID != nil:
			return clienterrorbus.Issue{}, errs.New(errs.NotFound, clienterrorbus.ErrNotFound)
		case sc.orgID != nil && (issue.OrgID == nil || *issue.OrgID != *sc.orgID):
			return clienterrorbus.Issue{}, errs.New(errs.NotFound, clienterrorbus.ErrNotFound)
		}
	}

	return issue, nil
}

// queryIssueGlobal handles GET /v1/client-errors/issues/{issue_id}.
func (a *app) queryIssueGlobal(ctx context.Context, r *http.Request) web.Encoder {
	if _, errResp := globalScope(ctx); errResp != nil {
		return errResp
	}

	issue, errResp := a.loadIssue(ctx, r, true)
	if errResp != nil {
		return errResp
	}

	events, err := a.clientErrorBus.QueryIssueEvents(ctx, issue.ID, 20)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryissueevents: %s", err)
	}

	detail := IssueDetail{Issue: toAppIssue(issue), Events: make([]Event, len(events)), Series: []Point{}}
	for i, e := range events {
		detail.Events[i] = toAppEvent(e)
	}
	if len(detail.Events) > 0 {
		latest := detail.Events[0]
		detail.LatestEvent = &latest
	}

	return detail
}
