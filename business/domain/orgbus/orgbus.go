// Package orgbus provides business access to the organization domain.
package orgbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/delegate"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound     = errors.New("org not found")
	ErrUniqueSlug   = errors.New("slug is not unique")
	ErrMemberExists = errors.New("user is already a member")
	ErrMemberNotFound = errors.New("member not found")
)

// Storer interface declares the behavior this package needs to persist and
// retrieve data.
type Storer interface {
	NewWithTx(tx sqldb.CommitRollbacker) (Storer, error)

	// Org CRUD
	Create(ctx context.Context, org Org) error
	Update(ctx context.Context, org Org) error
	Delete(ctx context.Context, org Org) error
	Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Org, error)
	Count(ctx context.Context, filter QueryFilter) (int, error)
	QueryByID(ctx context.Context, orgID uuid.UUID) (Org, error)
	QueryBySlug(ctx context.Context, slug string) (Org, error)
	QueryByUserID(ctx context.Context, userID uuid.UUID) ([]UserOrg, error)
	UpdateEnabled(ctx context.Context, orgID uuid.UUID, enabled bool) error

	// Membership
	AddMember(ctx context.Context, member OrgMember) error
	RemoveMember(ctx context.Context, memberID uuid.UUID) error
	UpdateMemberRole(ctx context.Context, memberID uuid.UUID, r role.Role) error
	QueryMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMember, error)
	QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]OrgMemberUser, error)
	QueryMemberByID(ctx context.Context, memberID uuid.UUID) (OrgMember, error)
	QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (OrgMemberUser, error)

	// Subscriptions
	CreateSubscription(ctx context.Context, sub Subscription) error
	UpdateSubscription(ctx context.Context, sub Subscription) error
	QuerySubscription(ctx context.Context, orgID uuid.UUID) (Subscription, error)
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error)

	// Org lifecycle
	Create(ctx context.Context, actorID uuid.UUID, nu NewOrg) (Org, error)
	Update(ctx context.Context, actorID uuid.UUID, org Org, uu UpdateOrg) (Org, error)
	Delete(ctx context.Context, actorID uuid.UUID, org Org) error
	Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Org, error)
	Count(ctx context.Context, filter QueryFilter) (int, error)
	QueryByID(ctx context.Context, orgID uuid.UUID) (Org, error)
	QueryBySlug(ctx context.Context, slug string) (Org, error)
	QueryByUserID(ctx context.Context, userID uuid.UUID) ([]UserOrg, error)
	Activate(ctx context.Context, orgID uuid.UUID) error
	Suspend(ctx context.Context, orgID uuid.UUID) error

	// Membership
	AddMember(ctx context.Context, actorID uuid.UUID, nm NewOrgMember) (OrgMember, error)
	RemoveMember(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID) error
	UpdateMemberRole(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID, r role.Role) (OrgMember, error)
	QueryMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMember, error)
	QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]OrgMemberUser, error)
	QueryMemberByID(ctx context.Context, memberID uuid.UUID) (OrgMember, error)
	QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (OrgMemberUser, error)

	// Subscriptions
	CreateSubscription(ctx context.Context, actorID uuid.UUID, ns NewSubscription) (Subscription, error)
	UpdateSubscription(ctx context.Context, actorID uuid.UUID, sub Subscription, us UpdateSubscription) (Subscription, error)
	QuerySubscription(ctx context.Context, orgID uuid.UUID) (Subscription, error)
}

// Extension is a function that wraps a new layer of business logic
// around the existing business logic.
type Extension func(ExtBusiness) ExtBusiness

// Business manages the set of APIs for organization access.
type Business struct {
	log        *logger.Logger
	storer     Storer
	delegate   *delegate.Delegate
	extensions []Extension
}

// NewBusiness constructs an org business API for use.
func NewBusiness(log *logger.Logger, delegate *delegate.Delegate, storer Storer, extensions ...Extension) ExtBusiness {
	b := ExtBusiness(&Business{
		log:        log,
		delegate:   delegate,
		storer:     storer,
		extensions: extensions,
	})

	for i := len(extensions) - 1; i >= 0; i-- {
		ext := extensions[i]
		if ext != nil {
			b = ext(b)
		}
	}

	return b
}

// NewWithTx constructs a new business value that will use the
// specified transaction in any store related calls.
func (b *Business) NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error) {
	storer, err := b.storer.NewWithTx(tx)
	if err != nil {
		return nil, err
	}

	return NewBusiness(b.log, b.delegate, storer, b.extensions...), nil
}

// Create adds a new organization to the system and makes the creator an ORG ADMIN.
func (b *Business) Create(ctx context.Context, actorID uuid.UUID, nu NewOrg) (Org, error) {
	now := time.Now()

	org := Org{
		ID:          uuid.New(),
		Name:        nu.Name,
		Slug:        nu.Slug,
		Enabled:     true,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.Create(ctx, org); err != nil {
		return Org{}, fmt.Errorf("create: %w", err)
	}

	member := OrgMember{
		MemberID: uuid.New(),
		OrgID:    org.ID,
		UserID:   actorID,
		Role:     role.OrgAdmin,
		JoinedAt: now,
	}

	if err := b.storer.AddMember(ctx, member); err != nil {
		return Org{}, fmt.Errorf("addmember: %w", err)
	}

	return org, nil
}

// Update modifies information about an organization.
func (b *Business) Update(ctx context.Context, actorID uuid.UUID, org Org, uu UpdateOrg) (Org, error) {
	if uu.Name != nil {
		org.Name = *uu.Name
	}
	if uu.Slug != nil {
		org.Slug = *uu.Slug
	}
	if uu.Enabled != nil {
		org.Enabled = *uu.Enabled
	}
	org.DateUpdated = time.Now()

	if err := b.storer.Update(ctx, org); err != nil {
		return Org{}, fmt.Errorf("update: %w", err)
	}

	return org, nil
}

// Delete removes an organization from the system.
func (b *Business) Delete(ctx context.Context, actorID uuid.UUID, org Org) error {
	if err := b.storer.Delete(ctx, org); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Query retrieves a list of existing organizations from the database.
func (b *Business) Query(ctx context.Context, filter QueryFilter, orderBy order.By, page page.Page) ([]Org, error) {
	orgs, err := b.storer.Query(ctx, filter, orderBy, page)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return orgs, nil
}

// Count returns the total number of organizations.
func (b *Business) Count(ctx context.Context, filter QueryFilter) (int, error) {
	count, err := b.storer.Count(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return count, nil
}

// QueryByID finds the organization identified by a given ID.
func (b *Business) QueryByID(ctx context.Context, orgID uuid.UUID) (Org, error) {
	org, err := b.storer.QueryByID(ctx, orgID)
	if err != nil {
		return Org{}, fmt.Errorf("querybyid: %w", err)
	}
	return org, nil
}

// QueryBySlug finds the organization identified by a given slug.
func (b *Business) QueryBySlug(ctx context.Context, slug string) (Org, error) {
	org, err := b.storer.QueryBySlug(ctx, slug)
	if err != nil {
		return Org{}, fmt.Errorf("querybyslug: %w", err)
	}
	return org, nil
}

// QueryByUserID returns all orgs the given user is a member of, including
// their role in each org. This is what the frontend calls after login to
// know which workspaces the user can switch into.
func (b *Business) QueryByUserID(ctx context.Context, userID uuid.UUID) ([]UserOrg, error) {
	orgs, err := b.storer.QueryByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("querybyuserid: %w", err)
	}
	return orgs, nil
}

// Activate enables an organization.
func (b *Business) Activate(ctx context.Context, orgID uuid.UUID) error {
	if err := b.storer.UpdateEnabled(ctx, orgID, true); err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	return nil
}

// Suspend disables an organization without deleting it.
func (b *Business) Suspend(ctx context.Context, orgID uuid.UUID) error {
	if err := b.storer.UpdateEnabled(ctx, orgID, false); err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	return nil
}

// =============================================================================
// Membership

// AddMember adds a user as a member of an organization with a given role.
func (b *Business) AddMember(ctx context.Context, actorID uuid.UUID, nm NewOrgMember) (OrgMember, error) {
	member := OrgMember{
		MemberID: uuid.New(),
		OrgID:    nm.OrgID,
		UserID:   nm.UserID,
		Role:     nm.Role,
		JoinedAt: time.Now(),
	}

	if err := b.storer.AddMember(ctx, member); err != nil {
		return OrgMember{}, fmt.Errorf("addmember: %w", err)
	}

	return member, nil
}

// RemoveMember removes a user from an organization.
func (b *Business) RemoveMember(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID) error {
	if err := b.storer.RemoveMember(ctx, memberID); err != nil {
		return fmt.Errorf("removemember: %w", err)
	}
	return nil
}

// UpdateMemberRole changes the role of a member within an organization.
func (b *Business) UpdateMemberRole(ctx context.Context, actorID uuid.UUID, memberID uuid.UUID, r role.Role) (OrgMember, error) {
	if err := b.storer.UpdateMemberRole(ctx, memberID, r); err != nil {
		return OrgMember{}, fmt.Errorf("updatememberrole: %w", err)
	}

	member, err := b.storer.QueryMemberByID(ctx, memberID)
	if err != nil {
		return OrgMember{}, fmt.Errorf("querymemberbyid: %w", err)
	}

	return member, nil
}

// QueryMemberByID returns a single org member by their membership ID.
func (b *Business) QueryMemberByID(ctx context.Context, memberID uuid.UUID) (OrgMember, error) {
	member, err := b.storer.QueryMemberByID(ctx, memberID)
	if err != nil {
		return OrgMember{}, fmt.Errorf("querymemberbyid: %w", err)
	}
	return member, nil
}

// QueryMemberWithUserByID returns a single org member joined with their user profile.
func (b *Business) QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (OrgMemberUser, error) {
	member, err := b.storer.QueryMemberWithUserByID(ctx, memberID)
	if err != nil {
		return OrgMemberUser{}, fmt.Errorf("querymemberwithUserbyid: %w", err)
	}
	return member, nil
}

// QueryMembers returns all members of an organization.
func (b *Business) QueryMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMember, error) {
	members, err := b.storer.QueryMembers(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querymembers: %w", err)
	}
	return members, nil
}

// QueryMembersWithUsers returns all members of an org with their full user
// profiles resolved in a single JOIN query.
func (b *Business) QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]OrgMemberUser, error) {
	members, err := b.storer.QueryMembersWithUsers(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querymemberswithusers: %w", err)
	}
	return members, nil
}

// =============================================================================
// Subscriptions

// CreateSubscription attaches a new subscription to an organization.
func (b *Business) CreateSubscription(ctx context.Context, actorID uuid.UUID, ns NewSubscription) (Subscription, error) {
	now := time.Now()

	sub := Subscription{
		SubscriptionID: uuid.New(),
		OrgID:          ns.OrgID,
		Plan:           ns.Plan,
		Status:         ns.Status,
		PeriodStart:    ns.PeriodStart,
		PeriodEnd:      ns.PeriodEnd,
		DateCreated:    now,
		DateUpdated:    now,
	}

	if err := b.storer.CreateSubscription(ctx, sub); err != nil {
		return Subscription{}, fmt.Errorf("createsubscription: %w", err)
	}

	return sub, nil
}

// UpdateSubscription modifies an existing subscription (e.g. on a Stripe webhook).
func (b *Business) UpdateSubscription(ctx context.Context, actorID uuid.UUID, sub Subscription, us UpdateSubscription) (Subscription, error) {
	if us.Plan != nil {
		sub.Plan = *us.Plan
	}
	if us.Status != nil {
		sub.Status = *us.Status
	}
	if us.PeriodStart != nil {
		sub.PeriodStart = *us.PeriodStart
	}
	if us.PeriodEnd != nil {
		sub.PeriodEnd = *us.PeriodEnd
	}
	sub.DateUpdated = time.Now()

	if err := b.storer.UpdateSubscription(ctx, sub); err != nil {
		return Subscription{}, fmt.Errorf("updatesubscription: %w", err)
	}

	return sub, nil
}

// QuerySubscription returns the active subscription for an organization.
func (b *Business) QuerySubscription(ctx context.Context, orgID uuid.UUID) (Subscription, error) {
	sub, err := b.storer.QuerySubscription(ctx, orgID)
	if err != nil {
		return Subscription{}, fmt.Errorf("querysubscription: %w", err)
	}
	return sub, nil
}
