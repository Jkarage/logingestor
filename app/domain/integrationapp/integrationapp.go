// Package integrationapp maintains the app layer api for the integration domain.
package integrationapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	integrationBus *integrationbus.Business
}

func newApp(integrationBus *integrationbus.Business) *app {
	return &app{integrationBus: integrationBus}
}

// listProviders handles GET /v1/integration-providers.
func (a *app) listProviders(ctx context.Context, r *http.Request) web.Encoder {
	providers, err := a.integrationBus.QueryProviders(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryproviders: %s", err)
	}
	return toAppProviders(providers)
}

// list handles GET /v1/orgs/{org_id}/integrations.
func (a *app) list(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	integrations, err := a.integrationBus.QueryByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	return toAppIntegrations(integrations)
}

// create handles POST /v1/orgs/{org_id}/integrations.
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var req NewIntegrationRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busNew, err := toBusNewIntegration(req)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}
	busNew.OrgID = orgID

	integration, err := a.integrationBus.Create(ctx, mid.GetSubjectID(ctx), busNew)
	if err != nil {
		if errors.Is(err, integrationbus.ErrUnknownProvider) {
			return errs.New(errs.InvalidArgument, integrationbus.ErrUnknownProvider)
		}
		if errors.Is(err, integrationbus.ErrDuplicateName) {
			return errs.New(errs.Aborted, integrationbus.ErrDuplicateName)
		}
		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	return toAppIntegration(integration)
}

// update handles PUT /v1/orgs/{org_id}/integrations/{integration_id}.
func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	var req UpdateIntegrationRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	integrationID, err := uuid.Parse(web.Param(r, "integration_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	integration, err := a.integrationBus.QueryByID(ctx, integrationID)
	if err != nil {
		if errors.Is(err, integrationbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: integrationID[%s]: %s", integrationID, err)
	}

	updated, err := a.integrationBus.Update(ctx, mid.GetSubjectID(ctx), integration, toBusUpdateIntegration(req))
	if err != nil {
		return errs.Errorf(errs.Internal, "update: integrationID[%s]: %s", integrationID, err)
	}

	return toAppIntegration(updated)
}

// delete handles DELETE /v1/orgs/{org_id}/integrations/{integration_id}.
func (a *app) delete(ctx context.Context, r *http.Request) web.Encoder {
	integrationID, err := uuid.Parse(web.Param(r, "integration_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	integration, err := a.integrationBus.QueryByID(ctx, integrationID)
	if err != nil {
		if errors.Is(err, integrationbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: integrationID[%s]: %s", integrationID, err)
	}

	if err := a.integrationBus.Delete(ctx, mid.GetSubjectID(ctx), integration); err != nil {
		return errs.Errorf(errs.Internal, "delete: integrationID[%s]: %s", integrationID, err)
	}

	return nil
}

// test handles POST /v1/orgs/{org_id}/integrations/{integration_id}/test.
func (a *app) test(ctx context.Context, r *http.Request) web.Encoder {
	integrationID, err := uuid.Parse(web.Param(r, "integration_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	integration, err := a.integrationBus.QueryByID(ctx, integrationID)
	if err != nil {
		if errors.Is(err, integrationbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: integrationID[%s]: %s", integrationID, err)
	}

	if err := a.integrationBus.Test(ctx, integration); err != nil {
		return errs.Errorf(errs.Internal, "test: integrationID[%s]: %s", integrationID, err)
	}

	return nil
}
