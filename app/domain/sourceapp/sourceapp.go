// Package sourceapp maintains the app layer api for the source domain.
package sourceapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	sourceBus  sourcebus.ExtBusiness
	projectBus projectbus.ExtBusiness
}

func newApp(sourceBus sourcebus.ExtBusiness, projectBus projectbus.ExtBusiness) *app {
	return &app{
		sourceBus:  sourceBus,
		projectBus: projectBus,
	}
}

// create handles POST /v1/orgs/{org_id}/sources.
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var ns NewSource
	if err := web.Decode(r, &ns); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if ns.Name == "" {
		return errs.New(errs.InvalidArgument, errors.New("name is required"))
	}

	// Bad kind: the repo's error set has no 422; surface as 400.
	if !sourcebus.ValidKind(ns.Kind) {
		return errs.Errorf(errs.InvalidArgument, "invalid kind %q", ns.Kind)
	}

	projectID, err := uuid.Parse(ns.ProjectID)
	if err != nil {
		return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
	}

	// The target project must exist and belong to this org (404 otherwise) so a
	// source can never be pointed at another tenant's project.
	project, err := a.projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("project not found in org"))
		}
		return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}
	if project.OrgID != orgID {
		return errs.New(errs.NotFound, errors.New("project not found in org"))
	}

	source, rawKey, err := a.sourceBus.Create(ctx, mid.GetSubjectID(ctx), sourcebus.NewSource{
		OrgID:     orgID,
		ProjectID: projectID,
		Kind:      ns.Kind,
		Name:      ns.Name,
	})
	if err != nil {
		if errors.Is(err, sourcebus.ErrDuplicateName) {
			return errs.New(errs.Aborted, sourcebus.ErrDuplicateName)
		}
		if errors.Is(err, sourcebus.ErrInvalidKind) {
			return errs.Errorf(errs.InvalidArgument, "invalid kind %q", ns.Kind)
		}
		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	return SourceCreated{
		ID:        source.ID.String(),
		Kind:      source.Kind,
		Name:      source.Name,
		ProjectID: source.ProjectID.String(),
		IsActive:  source.IsActive,
		CreatedAt: source.CreatedAt.Format(time.RFC3339),
		IngestKey: rawKey,
		KeyPrefix: source.KeyPrefix,
	}
}

// query handles GET /v1/orgs/{org_id}/sources.
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sources, err := a.sourceBus.QueryByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	return toAppSources(sources)
}

// disconnect handles DELETE /v1/orgs/{org_id}/sources/{source_id} — soft-disable.
func (a *app) disconnect(ctx context.Context, r *http.Request) web.Encoder {
	source, errResp := a.loadOrgSource(ctx, r)
	if errResp != nil {
		return errResp
	}

	if _, err := a.sourceBus.Disable(ctx, mid.GetSubjectID(ctx), source); err != nil {
		return errs.Errorf(errs.Internal, "disable: sourceID[%s]: %s", source.ID, err)
	}

	return Disconnected{Disconnected: true}
}

// rotateKey handles POST /v1/orgs/{org_id}/sources/{source_id}/rotate-key.
func (a *app) rotateKey(ctx context.Context, r *http.Request) web.Encoder {
	source, errResp := a.loadOrgSource(ctx, r)
	if errResp != nil {
		return errResp
	}

	updated, rawKey, err := a.sourceBus.RotateKey(ctx, mid.GetSubjectID(ctx), source)
	if err != nil {
		return errs.Errorf(errs.Internal, "rotatekey: sourceID[%s]: %s", source.ID, err)
	}

	return RotatedKey{IngestKey: rawKey, KeyPrefix: updated.KeyPrefix}
}

// loadOrgSource parses the org_id + source_id path params and loads the source,
// confirming it belongs to the org (404 otherwise). Returns a non-nil
// web.Encoder error response on failure.
func (a *app) loadOrgSource(ctx context.Context, r *http.Request) (sourcebus.Source, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return sourcebus.Source{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sourceID, err := uuid.Parse(web.Param(r, "source_id"))
	if err != nil {
		return sourcebus.Source{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	source, err := a.sourceBus.QueryByID(ctx, sourceID)
	if err != nil {
		if errors.Is(err, sourcebus.ErrNotFound) {
			return sourcebus.Source{}, errs.New(errs.NotFound, sourcebus.ErrNotFound)
		}
		return sourcebus.Source{}, errs.Errorf(errs.Internal, "querybyid: sourceID[%s]: %s", sourceID, err)
	}

	if source.OrgID != orgID {
		return sourcebus.Source{}, errs.New(errs.NotFound, fmt.Errorf("%w in org", sourcebus.ErrNotFound))
	}

	return source, nil
}
