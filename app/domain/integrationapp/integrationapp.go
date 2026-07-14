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
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	integrationBus *integrationbus.Business
	projectBus     projectbus.ExtBusiness
	userBus        userbus.ExtBusiness
	auditBus       auditbus.ExtBusiness
}

func newApp(integrationBus *integrationbus.Business, projectBus projectbus.ExtBusiness, userBus userbus.ExtBusiness, auditBus auditbus.ExtBusiness) *app {
	return &app{
		integrationBus: integrationBus,
		projectBus:     projectBus,
		userBus:        userBus,
		auditBus:       auditBus,
	}
}

// listProviders handles GET /v1/integration-providers (global catalog).
func (a *app) listProviders(ctx context.Context, r *http.Request) web.Encoder {
	providers, err := a.integrationBus.QueryProviders(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryproviders: %s", err)
	}
	return toAppProviders(providers)
}

// orgAggregate handles GET /v1/orgs/{org_id}/integrations — a read-only view of
// every connection across the org's projects (each row carries its projectId).
func (a *app) orgAggregate(ctx context.Context, r *http.Request) web.Encoder {
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

// =============================================================================
// Project-scoped connections

// list handles GET /v1/orgs/{org_id}/projects/{project_id}/integrations.
func (a *app) list(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	integrations, err := a.integrationBus.QueryByProject(ctx, projectID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyproject: projectID[%s]: %s", projectID, err)
	}

	return toAppIntegrations(integrations)
}

// create handles POST /v1/orgs/{org_id}/projects/{project_id}/integrations.
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var req NewIntegrationRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, projectID, errResp := a.orgProject(ctx, r)
	if errResp != nil {
		return errResp
	}

	busNew, err := toBusNewIntegration(req)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}
	busNew.OrgID = orgID
	busNew.ProjectID = projectID

	actorID := mid.GetSubjectID(ctx)

	integration, err := a.integrationBus.Create(ctx, actorID, busNew)
	if err != nil {
		if errors.Is(err, integrationbus.ErrUnknownProvider) {
			return errs.New(errs.InvalidArgument, integrationbus.ErrUnknownProvider)
		}
		if errors.Is(err, integrationbus.ErrDuplicateName) {
			return errs.New(errs.Aborted, integrationbus.ErrDuplicateName)
		}
		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     orgID,
		ObjID:     integration.ID,
		ObjDomain: domain.Integration,
		ActorID:   actorID,
		Action:    "integration.connected",
		Data:      map[string]string{"provider": integration.ProviderID, "name": integration.Name, "project_id": projectID.String()},
		Message:   "integration connected",
	})

	return toAppIntegration(integration)
}

// update handles PUT .../integrations/{integration_id}.
func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	var req UpdateIntegrationRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	integration, errResp := a.projectConnection(ctx, r)
	if errResp != nil {
		return errResp
	}

	updated, err := a.integrationBus.Update(ctx, mid.GetSubjectID(ctx), integration, toBusUpdateIntegration(req))
	if err != nil {
		return errs.Errorf(errs.Internal, "update: integrationID[%s]: %s", integration.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     updated.OrgID,
		ObjID:     updated.ID,
		ObjDomain: domain.Integration,
		ActorID:   mid.GetSubjectID(ctx),
		Action:    "integration.updated",
		Data:      map[string]string{"provider": updated.ProviderID, "name": updated.Name},
		Message:   "integration updated",
	})

	return toAppIntegration(updated)
}

// delete handles DELETE .../integrations/{integration_id} — soft-disable and
// suspend the rules bound to it.
func (a *app) delete(ctx context.Context, r *http.Request) web.Encoder {
	integration, errResp := a.projectConnection(ctx, r)
	if errResp != nil {
		return errResp
	}

	actorID := mid.GetSubjectID(ctx)
	if err := a.integrationBus.Disable(ctx, actorID, integration); err != nil {
		return errs.Errorf(errs.Internal, "disable: integrationID[%s]: %s", integration.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     integration.OrgID,
		ObjID:     integration.ID,
		ObjDomain: domain.Integration,
		ActorID:   actorID,
		Action:    "integration.disconnected",
		Data:      map[string]string{"provider": integration.ProviderID, "name": integration.Name},
		Message:   "integration disconnected",
	})

	return disconnectedResponse{Disconnected: true}
}

// test handles POST .../integrations/{integration_id}/test.
func (a *app) test(ctx context.Context, r *http.Request) web.Encoder {
	integration, errResp := a.projectConnection(ctx, r)
	if errResp != nil {
		return errResp
	}

	if err := a.integrationBus.Test(ctx, integration); err != nil {
		if errors.Is(err, integrationbus.ErrProviderRejected) {
			return errs.New(errs.BadGateway, err)
		}
		return errs.Errorf(errs.Internal, "test: integrationID[%s]: %s", integration.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     integration.OrgID,
		ObjID:     integration.ID,
		ObjDomain: domain.Integration,
		ActorID:   mid.GetSubjectID(ctx),
		Action:    "integration.tested",
		Data:      map[string]string{"provider": integration.ProviderID},
		Message:   "integration test sent",
	})

	return testResponse{OK: true, Message: fmt.Sprintf("Test event delivered to %s ✓", integration.ProviderID)}
}

// =============================================================================
// Project-scoped alert rules

// listRules handles GET /v1/orgs/{org_id}/projects/{project_id}/rules. Returns
// all of the project's rules (team-visible), each annotated with its owner.
func (a *app) listRules(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rules, err := a.integrationBus.QueryRulesByProject(ctx, projectID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryrulesbyproject: projectID[%s]: %s", projectID, err)
	}

	return a.renderRules(ctx, rules)
}

// listRulesByOrg handles GET /v1/orgs/{org_id}/rules — a read-only aggregate of
// every rule across the org's projects (admin view). Each row carries its
// projectId and owner so the caller can group by project and show ownerName.
// Writes are never accepted here; they stay on the project-scoped routes.
func (a *app) listRulesByOrg(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rules, err := a.integrationBus.QueryRulesByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryrulesbyorg: orgID[%s]: %s", orgID, err)
	}

	return a.renderRules(ctx, rules)
}

// renderRules annotates a rule slice with owner display info and wraps it as
// { "rules": [...] }.
func (a *app) renderRules(ctx context.Context, rules []integrationbus.AlertRule) web.Encoder {
	owners := a.ownerCache(ctx)
	list := make([]AppAlertRule, len(rules))
	for i, rule := range rules {
		list[i] = owners.annotate(toAppAlertRule(rule), rule.UserID)
	}

	type rulesResponse struct {
		Rules []AppAlertRule `json:"rules"`
	}
	return jsonEncoder{v: rulesResponse{Rules: list}}
}

// createRule handles POST /v1/orgs/{org_id}/projects/{project_id}/rules.
func (a *app) createRule(ctx context.Context, r *http.Request) web.Encoder {
	var req NewRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, projectID, errResp := a.orgProject(ctx, r)
	if errResp != nil {
		return errResp
	}

	actorID := mid.GetSubjectID(ctx)

	busNew, err := toBusNewRule(orgID, projectID, actorID, req)
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
		case errors.Is(err, integrationbus.ErrConnectionBadProject), errors.Is(err, integrationbus.ErrConnectionBadOrg):
			// Connection is not in this project — unprocessable per the spec.
			return errs.New(errs.FailedPrecondition, integrationbus.ErrConnectionBadProject)
		}
		return errs.Errorf(errs.Internal, "createrule: %s", err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     orgID,
		ObjID:     rule.ID,
		ObjDomain: domain.Rule,
		ActorID:   actorID,
		Action:    "rule.created",
		Data:      map[string]string{"name": rule.Name, "level": rule.Level, "project_id": projectID.String()},
		Message:   "alert rule created",
	})

	owners := a.ownerCache(ctx)
	return ruleResponse{Rule: owners.annotate(toAppAlertRule(rule), rule.UserID)}
}

// updateRule handles PUT .../rules/{rule_id}. Any project member with manage
// rights may edit; the owner (user_id) is preserved.
func (a *app) updateRule(ctx context.Context, r *http.Request) web.Encoder {
	var req UpdateRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	rule, errResp := a.projectRule(ctx, r)
	if errResp != nil {
		return errResp
	}

	busUpdate, err := toBusUpdateRule(req)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	updated, err := a.integrationBus.UpdateRule(ctx, rule, busUpdate)
	if err != nil {
		switch {
		case errors.Is(err, integrationbus.ErrInvalidLevel):
			return errs.New(errs.OutOfRange, integrationbus.ErrInvalidLevel)
		case errors.Is(err, integrationbus.ErrConnectionBadProject), errors.Is(err, integrationbus.ErrNotFound):
			return errs.New(errs.FailedPrecondition, integrationbus.ErrConnectionBadProject)
		}
		return errs.Errorf(errs.Internal, "updaterule: ruleID[%s]: %s", rule.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     rule.OrgID,
		ObjID:     rule.ID,
		ObjDomain: domain.Rule,
		ActorID:   mid.GetSubjectID(ctx),
		Action:    "rule.updated",
		Data:      map[string]string{"name": updated.Name, "level": updated.Level},
		Message:   "alert rule updated",
	})

	owners := a.ownerCache(ctx)
	return ruleResponse{Rule: owners.annotate(toAppAlertRule(updated), updated.UserID)}
}

// toggleRule handles PATCH .../rules/{rule_id}/toggle.
func (a *app) toggleRule(ctx context.Context, r *http.Request) web.Encoder {
	var req ToggleRuleRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	rule, errResp := a.projectRule(ctx, r)
	if errResp != nil {
		return errResp
	}

	updated, err := a.integrationBus.UpdateRule(ctx, rule, integrationbus.UpdateAlertRule{IsActive: &req.IsActive})
	if err != nil {
		return errs.Errorf(errs.Internal, "togglerule: ruleID[%s]: %s", rule.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     rule.OrgID,
		ObjID:     rule.ID,
		ObjDomain: domain.Rule,
		ActorID:   mid.GetSubjectID(ctx),
		Action:    "rule.toggled",
		Data:      map[string]bool{"is_active": req.IsActive},
		Message:   "alert rule toggled",
	})

	return toggleRuleResponse{ID: updated.ID.String(), IsActive: updated.IsActive}
}

// deleteRule handles DELETE .../rules/{rule_id}.
func (a *app) deleteRule(ctx context.Context, r *http.Request) web.Encoder {
	rule, errResp := a.projectRule(ctx, r)
	if errResp != nil {
		return errResp
	}

	actorID := mid.GetSubjectID(ctx)
	if err := a.integrationBus.DeleteRule(ctx, rule.ID); err != nil {
		return errs.Errorf(errs.Internal, "deleterule: ruleID[%s]: %s", rule.ID, err)
	}

	a.auditBus.Create(ctx, auditbus.NewAudit{ //nolint:errcheck
		OrgID:     rule.OrgID,
		ObjID:     rule.ID,
		ObjDomain: domain.Rule,
		ObjName:   rule.Name,
		ActorID:   actorID,
		Action:    "rule.deleted",
		Data:      map[string]string{"name": rule.Name, "level": rule.Level, "project_id": rule.ProjectID.String()},
		Message:   fmt.Sprintf("alert rule %q (level: %s) deleted", rule.Name, rule.Level),
	})

	return deleteRuleResponse{Deleted: true}
}

// =============================================================================
// Ownership helpers — enforce that a connection/rule belongs to the org+project
// on the path, so nothing can be read or written across tenants.

// orgProject parses and validates that {project_id} belongs to {org_id}.
func (a *app) orgProject(ctx context.Context, r *http.Request) (uuid.UUID, uuid.UUID, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	project, err := a.projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return uuid.UUID{}, uuid.UUID{}, errs.New(errs.NotFound, errors.New("project not found in org"))
		}
		return uuid.UUID{}, uuid.UUID{}, errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}
	if project.OrgID != orgID {
		return uuid.UUID{}, uuid.UUID{}, errs.New(errs.NotFound, errors.New("project not found in org"))
	}

	return orgID, projectID, nil
}

// projectConnection loads the {integration_id} connection and confirms it lives
// in the {org_id}/{project_id} on the path (404 otherwise).
func (a *app) projectConnection(ctx context.Context, r *http.Request) (integrationbus.Integration, web.Encoder) {
	_, projectID, errResp := a.orgProject(ctx, r)
	if errResp != nil {
		return integrationbus.Integration{}, errResp
	}

	id, err := uuid.Parse(web.Param(r, "integration_id"))
	if err != nil {
		return integrationbus.Integration{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	conn, err := a.integrationBus.QueryByID(ctx, id)
	if err != nil {
		if errors.Is(err, integrationbus.ErrNotFound) {
			return integrationbus.Integration{}, errs.New(errs.NotFound, integrationbus.ErrNotFound)
		}
		return integrationbus.Integration{}, errs.Errorf(errs.Internal, "querybyid: integrationID[%s]: %s", id, err)
	}
	if conn.ProjectID != projectID {
		return integrationbus.Integration{}, errs.New(errs.NotFound, integrationbus.ErrNotFound)
	}

	return conn, nil
}

// projectRule loads the {rule_id} rule and confirms it lives in the
// {org_id}/{project_id} on the path (404 otherwise).
func (a *app) projectRule(ctx context.Context, r *http.Request) (integrationbus.AlertRule, web.Encoder) {
	_, projectID, errResp := a.orgProject(ctx, r)
	if errResp != nil {
		return integrationbus.AlertRule{}, errResp
	}

	id, err := uuid.Parse(web.Param(r, "rule_id"))
	if err != nil {
		return integrationbus.AlertRule{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	rule, err := a.integrationBus.QueryRuleByID(ctx, id)
	if err != nil {
		if errors.Is(err, integrationbus.ErrRuleNotFound) {
			return integrationbus.AlertRule{}, errs.New(errs.NotFound, integrationbus.ErrRuleNotFound)
		}
		return integrationbus.AlertRule{}, errs.Errorf(errs.Internal, "queryrulebyid: ruleID[%s]: %s", id, err)
	}
	if rule.ProjectID != projectID {
		return integrationbus.AlertRule{}, errs.New(errs.NotFound, integrationbus.ErrRuleNotFound)
	}

	return rule, nil
}

// =============================================================================
// Owner display resolution

type ownerResolver struct {
	ctx     context.Context
	userBus userbus.ExtBusiness
	cache   map[uuid.UUID][2]string // userID -> {name, email}
}

func (a *app) ownerCache(ctx context.Context) *ownerResolver {
	return &ownerResolver{ctx: ctx, userBus: a.userBus, cache: make(map[uuid.UUID][2]string)}
}

// annotate fills ownerName/ownerEmail on the rule from its userID (best effort).
func (o *ownerResolver) annotate(rule AppAlertRule, userID *uuid.UUID) AppAlertRule {
	if userID == nil {
		return rule
	}
	if v, ok := o.cache[*userID]; ok {
		rule.OwnerName, rule.OwnerEmail = v[0], v[1]
		return rule
	}
	usr, err := o.userBus.QueryByID(o.ctx, *userID)
	if err != nil {
		o.cache[*userID] = [2]string{}
		return rule
	}
	name, email := usr.Name.String(), usr.Email.Address
	o.cache[*userID] = [2]string{name, email}
	rule.OwnerName, rule.OwnerEmail = name, email
	return rule
}

// jsonEncoder is a lightweight adapter to encode arbitrary values as JSON responses.
type jsonEncoder struct{ v any }

func (j jsonEncoder) Encode() ([]byte, string, error) {
	data, err := json.Marshal(j.v)
	return data, "application/json", err
}
