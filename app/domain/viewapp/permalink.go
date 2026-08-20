package viewapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// Permalink is a shareable pointer to a log or a query.
//
// It resolves to identifiers rather than to the log itself: a log old enough to
// have been purged by retention still resolves, and the client gets a 404 from
// GET /v1/projects/{projectId}/logs/{logId} that it can explain, instead of the
// link simply not working.
type Permalink struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	OrgID       string          `json:"orgId"`
	Kind        string          `json:"kind"`
	ProjectID   *string         `json:"projectId"`
	LogID       *string         `json:"logId"`
	Query       json.RawMessage `json:"query"`
	CreatedBy   string          `json:"createdBy"`
	CanEdit     bool            `json:"canEdit"`
	DateCreated string          `json:"dateCreated"`
}

// Encode implements the encoder interface.
func (app Permalink) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// HTTPStatus returns 201 when a permalink is created.
type permalinkCreated struct{ Permalink }

func (permalinkCreated) HTTPStatus() int { return http.StatusCreated }

// Permalinks is the list response shape.
type Permalinks struct {
	Permalinks []Permalink `json:"permalinks"`
}

// Encode implements the encoder interface.
func (app Permalinks) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// NewPermalink is the request body for creating one.
type NewPermalink struct {
	Kind      string          `json:"kind"`
	ProjectID string          `json:"projectId"`
	LogID     string          `json:"logId"`
	Query     json.RawMessage `json:"query"`
}

// Decode implements the decoder interface.
func (app *NewPermalink) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toAppPermalink(p viewbus.Permalink, canEdit bool) Permalink {
	out := Permalink{
		ID:          p.ID.String(),
		Slug:        p.Slug,
		OrgID:       p.OrgID.String(),
		Kind:        p.Kind,
		Query:       p.Query,
		CreatedBy:   p.CreatedBy.String(),
		CanEdit:     canEdit,
		DateCreated: p.DateCreated.Format(time.RFC3339),
	}

	if len(out.Query) == 0 {
		out.Query = json.RawMessage("{}")
	}
	if p.ProjectID != nil {
		s := p.ProjectID.String()
		out.ProjectID = &s
	}
	if p.LogID != nil {
		s := p.LogID.String()
		out.LogID = &s
	}

	return out
}

// createPermalink mints a shareable link.
// POST /v1/orgs/{org_id}/permalinks
func (a *app) createPermalink(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	var body NewPermalink
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	np := viewbus.NewPermalink{Kind: body.Kind, Query: body.Query}

	if body.ProjectID != "" {
		projectID, err := uuid.Parse(body.ProjectID)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
		}

		// A link may only point at a project the creator can read, or it would be
		// a way to hand someone a pointer into a project they were never granted.
		if _, ok := who.VisibleProjects[projectID]; !ok {
			return errs.New(errs.NotFound, errors.New("project not found in org"))
		}
		np.ProjectID = &projectID
	}

	if body.LogID != "" {
		logID, err := uuid.Parse(body.LogID)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid logId"))
		}
		np.LogID = &logID
	}

	p, err := a.viewBus.CreatePermalink(ctx, orgID, who.UserID, np)
	if err != nil {
		if resp := permalinkValidationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "createpermalink: orgID[%s]: %s", orgID, err)
	}

	return permalinkCreated{toAppPermalink(p, true)}
}

// queryPermalink resolves a slug.
// GET /v1/orgs/{org_id}/permalinks/{slug}
func (a *app) queryPermalink(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	p, err := a.viewBus.QueryPermalinkBySlug(ctx, orgID, web.Param(r, "slug"))
	if err != nil {
		if errors.Is(err, viewbus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("permalink not found"))
		}
		return errs.Errorf(errs.Internal, "querypermalink: orgID[%s]: %s", orgID, err)
	}

	// The slug is unguessable but it is not an authorization: a member who cannot
	// read the linked project must not learn what it points at.
	if p.ProjectID != nil {
		if _, ok := who.VisibleProjects[*p.ProjectID]; !ok {
			return errs.New(errs.NotFound, errors.New("permalink not found"))
		}
	}

	return toAppPermalink(p, viewbus.CanModify(p.CreatedBy, viewbus.VisibilityOrg, who))
}

// queryPermalinks lists the org's permalinks the caller may see.
// GET /v1/orgs/{org_id}/permalinks
func (a *app) queryPermalinks(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	links, err := a.viewBus.QueryPermalinksByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querypermalinks: orgID[%s]: %s", orgID, err)
	}

	out := make([]Permalink, 0, len(links))
	for _, p := range links {
		if p.ProjectID != nil {
			if _, ok := who.VisibleProjects[*p.ProjectID]; !ok {
				continue
			}
		}
		out = append(out, toAppPermalink(p, viewbus.CanModify(p.CreatedBy, viewbus.VisibilityOrg, who)))
	}

	return Permalinks{Permalinks: out}
}

// deletePermalink revokes a link.
// DELETE /v1/orgs/{org_id}/permalinks/{slug}
func (a *app) deletePermalink(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	p, err := a.viewBus.QueryPermalinkBySlug(ctx, orgID, web.Param(r, "slug"))
	if err != nil {
		if errors.Is(err, viewbus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("permalink not found"))
		}
		return errs.Errorf(errs.Internal, "querypermalink: orgID[%s]: %s", orgID, err)
	}

	// A permalink is shared with the org, so an org admin may revoke anyone's —
	// the same rule the shared saved views follow.
	if !viewbus.CanModify(p.CreatedBy, viewbus.VisibilityOrg, who) {
		return errs.New(errs.PermissionDenied, errors.New("only the creator or an org admin may delete this permalink"))
	}

	if err := a.viewBus.DeletePermalink(ctx, p.ID); err != nil {
		return errs.Errorf(errs.Internal, "deletepermalink: id[%s]: %s", p.ID, err)
	}

	// 204: the same shape the saved-view and dashboard deletes return.
	return nil
}

// permalinkValidationErr maps permalink validation failures to 400s.
func permalinkValidationErr(err error) web.Encoder {
	switch {
	case errors.Is(err, viewbus.ErrPermalinkKind),
		errors.Is(err, viewbus.ErrPermalinkLogID),
		errors.Is(err, viewbus.ErrPermalinkQueryID),
		errors.Is(err, viewbus.ErrDefTooLarge):
		return errs.New(errs.InvalidArgument, err)
	}

	return nil
}
