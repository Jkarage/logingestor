// Package orgotel provides an extension for orgbus that adds otel tracking.
package orgotel

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/otel"
)

// Extension provides a wrapper for otel functionality around the orgbus.
type Extension struct {
	bus orgbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the orgbus with otel.
func NewExtension() orgbus.Extension {
	return func(bus orgbus.ExtBusiness) orgbus.ExtBusiness {
		return &Extension{bus: bus}
	}
}

func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (orgbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

// =============================================================================
// Org lifecycle

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, nu orgbus.NewOrg) (orgbus.Org, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.create")
	defer span.End()

	return ext.bus.Create(ctx, actorID, nu)
}

func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, org orgbus.Org, uu orgbus.UpdateOrg) (orgbus.Org, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.update")
	defer span.End()

	return ext.bus.Update(ctx, actorID, org, uu)
}

func (ext *Extension) Delete(ctx context.Context, actorID uuid.UUID, org orgbus.Org) error {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.delete")
	defer span.End()

	return ext.bus.Delete(ctx, actorID, org)
}

func (ext *Extension) Query(ctx context.Context, filter orgbus.QueryFilter, orderBy order.By, page page.Page) ([]orgbus.Org, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.query")
	defer span.End()

	return ext.bus.Query(ctx, filter, orderBy, page)
}

func (ext *Extension) Count(ctx context.Context, filter orgbus.QueryFilter) (int, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.count")
	defer span.End()

	return ext.bus.Count(ctx, filter)
}

func (ext *Extension) QueryByID(ctx context.Context, orgID uuid.UUID) (orgbus.Org, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querybyid")
	defer span.End()

	return ext.bus.QueryByID(ctx, orgID)
}

func (ext *Extension) QueryBySlug(ctx context.Context, slug string) (orgbus.Org, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querybyslug")
	defer span.End()

	return ext.bus.QueryBySlug(ctx, slug)
}

func (ext *Extension) QueryByUserID(ctx context.Context, userID uuid.UUID) ([]orgbus.UserOrg, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querybyuserid")
	defer span.End()

	return ext.bus.QueryByUserID(ctx, userID)
}

func (ext *Extension) Activate(ctx context.Context, orgID uuid.UUID) error {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.activate")
	defer span.End()

	return ext.bus.Activate(ctx, orgID)
}

func (ext *Extension) Suspend(ctx context.Context, orgID uuid.UUID) error {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.suspend")
	defer span.End()

	return ext.bus.Suspend(ctx, orgID)
}

// =============================================================================
// Membership

func (ext *Extension) AddMember(ctx context.Context, actorID uuid.UUID, nm orgbus.NewOrgMember) (orgbus.OrgMember, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.addmember")
	defer span.End()

	return ext.bus.AddMember(ctx, actorID, nm)
}

func (ext *Extension) RemoveMember(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID) error {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.removemember")
	defer span.End()

	return ext.bus.RemoveMember(ctx, actorID, memberID)
}

func (ext *Extension) UpdateMemberRole(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID, r role.Role) (orgbus.OrgMember, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.updatememberrole")
	defer span.End()

	return ext.bus.UpdateMemberRole(ctx, actorID, memberID, r)
}

func (ext *Extension) QueryMembers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMember, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querymembers")
	defer span.End()

	return ext.bus.QueryMembers(ctx, orgID)
}

func (ext *Extension) QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMemberUser, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querymemberswithusers")
	defer span.End()

	return ext.bus.QueryMembersWithUsers(ctx, orgID)
}

func (ext *Extension) QueryMemberByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMember, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querymemberbyid")
	defer span.End()

	return ext.bus.QueryMemberByID(ctx, memberID)
}

func (ext *Extension) QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMemberUser, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querymemberwithUserbyid")
	defer span.End()

	return ext.bus.QueryMemberWithUserByID(ctx, memberID)
}

// =============================================================================
// Subscriptions

func (ext *Extension) CreateSubscription(ctx context.Context, actorID uuid.UUID, ns orgbus.NewSubscription) (orgbus.Subscription, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.createsubscription")
	defer span.End()

	return ext.bus.CreateSubscription(ctx, actorID, ns)
}

func (ext *Extension) UpdateSubscription(ctx context.Context, actorID uuid.UUID, sub orgbus.Subscription, us orgbus.UpdateSubscription) (orgbus.Subscription, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.updatesubscription")
	defer span.End()

	return ext.bus.UpdateSubscription(ctx, actorID, sub, us)
}

func (ext *Extension) QuerySubscription(ctx context.Context, orgID uuid.UUID) (orgbus.Subscription, error) {
	ctx, span := otel.AddSpan(ctx, "business.orgbus.querysubscription")
	defer span.End()

	return ext.bus.QuerySubscription(ctx, orgID)
}
