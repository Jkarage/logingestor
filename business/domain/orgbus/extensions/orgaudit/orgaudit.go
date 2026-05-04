// Package orgaudit provides an extension for orgbus that adds audit logging.
package orgaudit

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jkarage/logingestor/business/types/role"
)

// Extension provides a wrapper for audit functionality around the orgbus.
type Extension struct {
	bus      orgbus.ExtBusiness
	auditBus auditbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the orgbus with audit.
func NewExtension(auditBus auditbus.ExtBusiness) orgbus.Extension {
	return func(bus orgbus.ExtBusiness) orgbus.ExtBusiness {
		return &Extension{
			bus:      bus,
			auditBus: auditBus,
		}
	}
}

// NewWithTx does not apply auditing.
func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (orgbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

// =============================================================================
// Org lifecycle

func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, nu orgbus.NewOrg) (orgbus.Org, error) {
	org, err := ext.bus.Create(ctx, actorID, nu)
	if err != nil {
		return orgbus.Org{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     org.ID,
		ObjID:     org.ID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "org.created",
		Data:      nu,
		Message:   "org created",
	}); err != nil {
		return orgbus.Org{}, err
	}

	return org, nil
}

func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, org orgbus.Org, uu orgbus.UpdateOrg) (orgbus.Org, error) {
	old := org
	org, err := ext.bus.Update(ctx, actorID, org, uu)
	if err != nil {
		return orgbus.Org{}, err
	}

	// Only a name change → org.renamed; any other field touched → org.updated.
	onlyName := uu.Name != nil && uu.Slug == nil && uu.Enabled == nil

	action := "org.updated"
	message := "org updated"
	var data any = uu

	if onlyName {
		action = "org.renamed"
		message = "org renamed"
		data = map[string]any{"name": org.Name.String(), "old_name": old.Name.String()}
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     org.ID,
		ObjID:     org.ID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    action,
		Data:      data,
		Message:   message,
	}); err != nil {
		return orgbus.Org{}, err
	}

	return org, nil
}

func (ext *Extension) Delete(ctx context.Context, actorID uuid.UUID, org orgbus.Org) error {
	if err := ext.bus.Delete(ctx, actorID, org); err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     org.ID,
		ObjID:     org.ID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "org.deleted",
		Data:      map[string]any{"name": org.Name.String(), "slug": org.Slug},
		Message:   "org deleted",
	}); err != nil {
		return err
	}

	return nil
}

// Query does not apply auditing.
func (ext *Extension) Query(ctx context.Context, filter orgbus.QueryFilter, orderBy order.By, page page.Page) ([]orgbus.Org, error) {
	return ext.bus.Query(ctx, filter, orderBy, page)
}

// Count does not apply auditing.
func (ext *Extension) Count(ctx context.Context, filter orgbus.QueryFilter) (int, error) {
	return ext.bus.Count(ctx, filter)
}

// QueryByID does not apply auditing.
func (ext *Extension) QueryByID(ctx context.Context, orgID uuid.UUID) (orgbus.Org, error) {
	return ext.bus.QueryByID(ctx, orgID)
}

// QueryBySlug does not apply auditing.
func (ext *Extension) QueryBySlug(ctx context.Context, slug string) (orgbus.Org, error) {
	return ext.bus.QueryBySlug(ctx, slug)
}

// QueryByUserID does not apply auditing.
func (ext *Extension) QueryByUserID(ctx context.Context, userID uuid.UUID) ([]orgbus.UserOrg, error) {
	return ext.bus.QueryByUserID(ctx, userID)
}

func (ext *Extension) Activate(ctx context.Context, orgID uuid.UUID) error {
	return ext.bus.Activate(ctx, orgID)
}

func (ext *Extension) Suspend(ctx context.Context, orgID uuid.UUID) error {
	org, err := ext.bus.QueryByID(ctx, orgID)
	if err != nil {
		return err
	}

	if err := ext.bus.Suspend(ctx, orgID); err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     org.ID,
		ObjID:     org.ID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   orgID,
		Action:    "org.suspended",
		Data:      nil,
		Message:   "org suspended",
	}); err != nil {
		return err
	}

	return nil
}

// =============================================================================
// Membership

func (ext *Extension) AddMember(ctx context.Context, actorID uuid.UUID, nm orgbus.NewOrgMember) (orgbus.OrgMember, error) {
	member, err := ext.bus.AddMember(ctx, actorID, nm)
	if err != nil {
		return orgbus.OrgMember{}, err
	}

	org, err := ext.bus.QueryByID(ctx, nm.OrgID)
	if err != nil {
		return orgbus.OrgMember{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     nm.OrgID,
		ObjID:     member.MemberID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "user.joined",
		Data:      nm,
		Message:   "member added to org",
	}); err != nil {
		return orgbus.OrgMember{}, err
	}

	return member, nil
}

func (ext *Extension) RemoveMember(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID) error {
	member, err := ext.bus.QueryMemberWithUserByID(ctx, memberID)
	if err != nil {
		return err
	}

	org, err := ext.bus.QueryByID(ctx, member.OrgID)
	if err != nil {
		return err
	}

	if err := ext.bus.RemoveMember(ctx, actorID, memberID); err != nil {
		return err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     member.OrgID,
		ObjID:     memberID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "user.removed",
		Data:      map[string]string{"name": member.Name.String(), "email": member.Email, "role": member.Role.String()},
		Message:   "member removed from org",
	}); err != nil {
		return err
	}

	return nil
}

func (ext *Extension) UpdateMemberRole(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID, r role.Role) (orgbus.OrgMember, error) {
	before, err := ext.bus.QueryMemberByID(ctx, memberID)
	if err != nil {
		return orgbus.OrgMember{}, err
	}

	member, err := ext.bus.UpdateMemberRole(ctx, actorID, memberID, r)
	if err != nil {
		return orgbus.OrgMember{}, err
	}

	org, err := ext.bus.QueryByID(ctx, member.OrgID)
	if err != nil {
		return orgbus.OrgMember{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     member.OrgID,
		ObjID:     memberID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "user.role_changed",
		Data:      map[string]string{"old_role": before.Role.String(), "new_role": r.String()},
		Message:   "member role updated",
	}); err != nil {
		return orgbus.OrgMember{}, err
	}

	return member, nil
}

// QueryMembers does not apply auditing.
func (ext *Extension) QueryMembers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMember, error) {
	return ext.bus.QueryMembers(ctx, orgID)
}

// QueryMemberByID does not apply auditing.
func (ext *Extension) QueryMemberByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMember, error) {
	return ext.bus.QueryMemberByID(ctx, memberID)
}

// QueryMemberWithUserByID does not apply auditing.
func (ext *Extension) QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMemberUser, error) {
	return ext.bus.QueryMemberWithUserByID(ctx, memberID)
}

// QueryMembersWithUsers does not apply auditing.
func (ext *Extension) QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMemberUser, error) {
	return ext.bus.QueryMembersWithUsers(ctx, orgID)
}

// =============================================================================
// Subscriptions

func (ext *Extension) CreateSubscription(ctx context.Context, actorID uuid.UUID, ns orgbus.NewSubscription) (orgbus.Subscription, error) {
	sub, err := ext.bus.CreateSubscription(ctx, actorID, ns)
	if err != nil {
		return orgbus.Subscription{}, err
	}

	org, err := ext.bus.QueryByID(ctx, ns.OrgID)
	if err != nil {
		return orgbus.Subscription{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     ns.OrgID,
		ObjID:     sub.SubscriptionID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "subscription_created",
		Data:      ns,
		Message:   "subscription created",
	}); err != nil {
		return orgbus.Subscription{}, err
	}

	return sub, nil
}

func (ext *Extension) UpdateSubscription(ctx context.Context, actorID uuid.UUID, sub orgbus.Subscription, us orgbus.UpdateSubscription) (orgbus.Subscription, error) {
	sub, err := ext.bus.UpdateSubscription(ctx, actorID, sub, us)
	if err != nil {
		return orgbus.Subscription{}, err
	}

	org, err := ext.bus.QueryByID(ctx, sub.OrgID)
	if err != nil {
		return orgbus.Subscription{}, err
	}

	if _, err := ext.auditBus.Create(ctx, auditbus.NewAudit{
		OrgID:     sub.OrgID,
		ObjID:     sub.SubscriptionID,
		ObjDomain: domain.Org,
		ObjName:   org.Name.String(),
		ActorID:   actorID,
		Action:    "subscription_updated",
		Data:      us,
		Message:   "subscription updated",
	}); err != nil {
		return orgbus.Subscription{}, err
	}

	return sub, nil
}

// QuerySubscription does not apply auditing.
func (ext *Extension) QuerySubscription(ctx context.Context, orgID uuid.UUID) (orgbus.Subscription, error) {
	return ext.bus.QuerySubscription(ctx, orgID)
}
