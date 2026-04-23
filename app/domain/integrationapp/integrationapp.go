// Package integrationapp maintains the app layer api for the integration domain.
package integrationapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// Soft-disables the integration and suspends all associated rules.
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

	if err := a.integrationBus.Disable(ctx, mid.GetSubjectID(ctx), integration); err != nil {
		return errs.Errorf(errs.Internal, "disable: integrationID[%s]: %s", integrationID, err)
	}

	return disconnectedResponse{Disconnected: true}
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
		if errors.Is(err, integrationbus.ErrProviderRejected) {
			return errs.New(errs.BadGateway, err)
		}
		return errs.Errorf(errs.Internal, "test: integrationID[%s]: %s", integrationID, err)
	}

	return testResponse{
		OK:      true,
		Message: fmt.Sprintf("Test event delivered to %s ✓", integration.ProviderID),
	}
}

// =============================================================================
// Alert Rule handlers

// listRules handles GET /v1/orgs/{org_id}/rules.
func (a *app) listRules(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rules, err := a.integrationBus.QueryRulesByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryrulesbyorg: orgID[%s]: %s", orgID, err)
	}

	list := make([]AppAlertRule, len(rules))
	for i, rule := range rules {
		list[i] = toAppAlertRule(rule)
	}

	type rulesResponse struct {
		Rules []AppAlertRule `json:"rules"`
	}
	return jsonEncoder{v: rulesResponse{Rules: list}}
}

// createRule handles POST /v1/orgs/{org_id}/rules.
func (a *app) createRule(ctx context.Context, r *http.Request) web.Encoder {
	var req NewRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	busNew, err := toBusNewRule(orgID, req)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	rule, err := a.integrationBus.CreateRule(ctx, busNew)
	if err != nil {
		switch {
		case errors.Is(err, integrationbus.ErrInvalidLevel):
			return errs.New(errs.OutOfRange, integrationbus.ErrInvalidLevel)
		case errors.Is(err, integrationbus.ErrNotFound):
			return errs.New(errs.NotFound, errors.New("connection not found"))
		case errors.Is(err, integrationbus.ErrConnectionBadOrg):
			return errs.New(errs.NotFound, errors.New("connection not found"))
		}
		return errs.Errorf(errs.Internal, "createrule: %s", err)
	}

	return ruleResponse{Rule: toAppAlertRule(rule)}
}

// updateRule handles PUT /v1/orgs/{org_id}/rules/{rule_id}.
func (a *app) updateRule(ctx context.Context, r *http.Request) web.Encoder {
	var req UpdateRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	ruleID, err := uuid.Parse(web.Param(r, "rule_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rule, err := a.integrationBus.QueryRuleByID(ctx, ruleID)
	if err != nil {
		if errors.Is(err, integrationbus.ErrRuleNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "queryrulebyid: ruleID[%s]: %s", ruleID, err)
	}

	busUpdate, err := toBusUpdateRule(req)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	updated, err := a.integrationBus.UpdateRule(ctx, rule, busUpdate)
	if err != nil {
		if errors.Is(err, integrationbus.ErrInvalidLevel) {
			return errs.New(errs.OutOfRange, integrationbus.ErrInvalidLevel)
		}
		return errs.Errorf(errs.Internal, "updaterule: ruleID[%s]: %s", ruleID, err)
	}

	return ruleResponse{Rule: toAppAlertRule(updated)}
}

// toggleRule handles PATCH /v1/orgs/{org_id}/rules/{rule_id}/toggle.
func (a *app) toggleRule(ctx context.Context, r *http.Request) web.Encoder {
	var req ToggleRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	ruleID, err := uuid.Parse(web.Param(r, "rule_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rule, err := a.integrationBus.QueryRuleByID(ctx, ruleID)
	if err != nil {
		if errors.Is(err, integrationbus.ErrRuleNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "queryrulebyid: ruleID[%s]: %s", ruleID, err)
	}

	updated, err := a.integrationBus.UpdateRule(ctx, rule, integrationbus.UpdateAlertRule{
		IsActive: &req.IsActive,
	})
	if err != nil {
		return errs.Errorf(errs.Internal, "togglerule: ruleID[%s]: %s", ruleID, err)
	}

	return toggleRuleResponse{ID: updated.ID.String(), IsActive: updated.IsActive}
}

// deleteRule handles DELETE /v1/orgs/{org_id}/rules/{rule_id}.
func (a *app) deleteRule(ctx context.Context, r *http.Request) web.Encoder {
	ruleID, err := uuid.Parse(web.Param(r, "rule_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if _, err := a.integrationBus.QueryRuleByID(ctx, ruleID); err != nil {
		if errors.Is(err, integrationbus.ErrRuleNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "queryrulebyid: ruleID[%s]: %s", ruleID, err)
	}

	if err := a.integrationBus.DeleteRule(ctx, ruleID); err != nil {
		return errs.Errorf(errs.Internal, "deleterule: ruleID[%s]: %s", ruleID, err)
	}

	return deleteRuleResponse{Deleted: true}
}

// jsonEncoder is a lightweight adapter to encode arbitrary values as JSON responses.
type jsonEncoder struct{ v any }

func (j jsonEncoder) Encode() ([]byte, string, error) {
	data, err := json.Marshal(j.v)
	return data, "application/json", err
}
