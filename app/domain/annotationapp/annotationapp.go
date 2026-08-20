package annotationapp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	annotationBus *annotationbus.Business
	logBus        logbus.ExtBusiness
	orgBus        orgbus.ExtBusiness
	projectBus    projectbus.ExtBusiness
}

func newApp(cfg Config) *app {
	return &app{
		annotationBus: cfg.AnnotationBus,
		logBus:        cfg.LogBus,
		orgBus:        cfg.OrgBus,
		projectBus:    cfg.ProjectBus,
	}
}

// caller is the access context for a request: who is asking, whether they
// administer this org, and which projects they may read.
type caller struct {
	userID   uuid.UUID
	orgAdmin bool
	projects map[uuid.UUID]struct{}
}

// resolve builds the access context for the org named in the path.
func (a *app) resolve(ctx context.Context, r *http.Request) (uuid.UUID, caller, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return uuid.UUID{}, caller{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	userID, err := mid.GetUserID(ctx)
	if err != nil {
		return uuid.UUID{}, caller{}, errs.New(errs.Unauthenticated, err)
	}

	c := caller{userID: userID}

	// A global SUPER ADMIN is treated as an org admin here, matching the bypass
	// the project authorization middleware applies.
	for _, r := range mid.GetClaims(ctx).Roles {
		if r == role.Admin.String() {
			c.orgAdmin = true
		}
	}

	if !c.orgAdmin {
		orgs, err := a.orgBus.QueryByUserID(ctx, userID)
		if err != nil {
			return uuid.UUID{}, caller{}, errs.Errorf(errs.Internal, "querybyuserid: %s", err)
		}
		for _, o := range orgs {
			if o.ID == orgID && o.Role == role.OrgAdmin {
				c.orgAdmin = true
				break
			}
		}
	}

	var projects []projectbus.Project
	if c.orgAdmin {
		projects, err = a.projectBus.QueryByOrg(ctx, orgID)
	} else {
		projects, err = a.projectBus.QueryVisibleByOrg(ctx, orgID, userID)
	}
	if err != nil {
		return uuid.UUID{}, caller{}, errs.Errorf(errs.Internal, "visible projects: %s", err)
	}

	c.projects = make(map[uuid.UUID]struct{}, len(projects))
	for _, p := range projects {
		c.projects[p.ID] = struct{}{}
	}

	return orgID, c, nil
}

// create stores a note.
// POST /v1/orgs/{org_id}/annotations
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	orgID, c, errResp := a.resolve(ctx, r)
	if errResp != nil {
		return errResp
	}

	var body NewAnnotation
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	projectID, err := uuid.Parse(body.ProjectID)
	if err != nil {
		return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
	}
	if _, ok := c.projects[projectID]; !ok {
		return errs.New(errs.NotFound, errors.New("project not found in org"))
	}

	na := annotationbus.NewAnnotation{ProjectID: projectID}

	switch {
	case body.LogID != "":
		logID, err := uuid.Parse(body.LogID)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid logId"))
		}

		// The log must exist in the named project, and its timestamp becomes the
		// note's anchor so the two cannot drift apart on a chart.
		l, err := a.logBus.QueryByID(ctx, logID)
		if err != nil {
			if errors.Is(err, logbus.ErrNotFound) {
				return errs.New(errs.NotFound, errors.New("log not found"))
			}
			return errs.Errorf(errs.Internal, "querybyid: logID[%s]: %s", logID, err)
		}
		if l.ProjectID != projectID {
			return errs.New(errs.NotFound, errors.New("log not found"))
		}

		na.LogID = &logID
		na.TS = l.Timestamp

	case body.TS != "":
		ts, err := time.Parse(time.RFC3339, body.TS)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'ts': want RFC3339"))
		}
		na.TS = ts

	default:
		// A marker with no time given is a marker for now, which is what
		// "annotate this deploy" means in practice.
		na.TS = time.Now()
	}

	na.Body = body.Body

	out, err := a.annotationBus.Create(ctx, orgID, c.userID, na)
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "create: orgID[%s]: %s", orgID, err)
	}

	return created{toAppAnnotation(out, true)}
}

// query lists notes.
// GET /v1/orgs/{org_id}/annotations?projectId&logId&from&to&limit
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	_, c, errResp := a.resolve(ctx, r)
	if errResp != nil {
		return errResp
	}

	q := r.URL.Query()
	filter := annotationbus.Filter{}

	if v := q.Get("projectId"); v != "" {
		projectID, err := uuid.Parse(v)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
		}
		if _, ok := c.projects[projectID]; !ok {
			return errs.New(errs.NotFound, errors.New("project not found in org"))
		}
		filter.ProjectIDs = []uuid.UUID{projectID}
	} else {
		// No project given reads every project the caller can see, so the org
		// timeline is one request.
		filter.ProjectIDs = make([]uuid.UUID, 0, len(c.projects))
		for id := range c.projects {
			filter.ProjectIDs = append(filter.ProjectIDs, id)
		}
	}

	if v := q.Get("logId"); v != "" {
		logID, err := uuid.Parse(v)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid logId"))
		}
		filter.LogID = &logID
	}

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
			return errs.Errorf(errs.InvalidArgument, "invalid '%s': want RFC3339", f.name)
		}
		*f.dst = &t
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		filter.Limit = n
	}

	notes, err := a.annotationBus.Query(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	out := make([]Annotation, len(notes))
	for i, n := range notes {
		out[i] = toAppAnnotation(n, annotationbus.CanModify(n, c.userID, c.orgAdmin))
	}

	return Annotations{Annotations: out}
}

// update edits a note's text.
// PATCH /v1/orgs/{org_id}/annotations/{annotation_id}
func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	note, c, errResp := a.load(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !annotationbus.CanModify(note, c.userID, c.orgAdmin) {
		return errs.New(errs.PermissionDenied, errors.New("only the author or an org admin may edit this annotation"))
	}

	var body UpdateAnnotation
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	out, err := a.annotationBus.Update(ctx, note, annotationbus.UpdateAnnotation{Body: body.Body})
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "update: id[%s]: %s", note.ID, err)
	}

	return toAppAnnotation(out, true)
}

// remove deletes a note.
// DELETE /v1/orgs/{org_id}/annotations/{annotation_id}
func (a *app) remove(ctx context.Context, r *http.Request) web.Encoder {
	note, c, errResp := a.load(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !annotationbus.CanModify(note, c.userID, c.orgAdmin) {
		return errs.New(errs.PermissionDenied, errors.New("only the author or an org admin may delete this annotation"))
	}

	if err := a.annotationBus.Delete(ctx, note.ID); err != nil {
		return errs.Errorf(errs.Internal, "delete: id[%s]: %s", note.ID, err)
	}

	return nil
}

// load fetches the annotation named in the path and confirms the caller may see
// it. A note in another org, or in a project they cannot read, is reported as
// not found rather than forbidden so the endpoint never confirms it exists.
func (a *app) load(ctx context.Context, r *http.Request) (annotationbus.Annotation, caller, web.Encoder) {
	orgID, c, errResp := a.resolve(ctx, r)
	if errResp != nil {
		return annotationbus.Annotation{}, caller{}, errResp
	}

	id, err := uuid.Parse(web.Param(r, "annotation_id"))
	if err != nil {
		return annotationbus.Annotation{}, caller{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	note, err := a.annotationBus.QueryByID(ctx, id)
	if err != nil {
		if errors.Is(err, annotationbus.ErrNotFound) {
			return annotationbus.Annotation{}, caller{}, errs.New(errs.NotFound, annotationbus.ErrNotFound)
		}
		return annotationbus.Annotation{}, caller{}, errs.Errorf(errs.Internal, "querybyid: id[%s]: %s", id, err)
	}

	if note.OrgID != orgID {
		return annotationbus.Annotation{}, caller{}, errs.New(errs.NotFound, annotationbus.ErrNotFound)
	}
	if _, ok := c.projects[note.ProjectID]; !ok {
		return annotationbus.Annotation{}, caller{}, errs.New(errs.NotFound, annotationbus.ErrNotFound)
	}

	return note, c, nil
}

// validationErr maps the business validation errors to 400 responses.
func validationErr(err error) web.Encoder {
	switch {
	case errors.Is(err, annotationbus.ErrBodyRequired),
		errors.Is(err, annotationbus.ErrBodyTooLong):
		return errs.New(errs.InvalidArgument, err)
	}

	return nil
}
