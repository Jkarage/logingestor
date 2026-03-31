package orgbus

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
)

// Org represents an organization (workspace) in the system.
type Org struct {
	ID          uuid.UUID
	Name        name.Name
	Slug        string
	Enabled     bool
	DateCreated time.Time
	DateUpdated time.Time
}

// NewOrg contains information needed to create a new organization.
type NewOrg struct {
	Name name.Name
	Slug string
}

// UpdateOrg contains information needed to update an organization.
type UpdateOrg struct {
	Name    *name.Name
	Slug    *string
	Enabled *bool
}

// =============================================================================
// Membership

// OrgMember represents a user's membership in an organization.
// The role is scoped to this membership row — a user can be admin in org A
// and viewer in org B.
type OrgMember struct {
	MemberID uuid.UUID
	OrgID    uuid.UUID
	UserID   uuid.UUID
	Role     role.Role
	JoinedAt time.Time
}

// NewOrgMember contains the information needed to add a member to an org.
type NewOrgMember struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	Role   role.Role
}

// UpdateOrgMember contains the fields that can be changed on a membership row.
type UpdateOrgMember struct {
	Role role.Role
}

// =============================================================================
// Subscriptions

// SubscriptionPlan represents the billing plan for an organization.
type SubscriptionPlan struct {
	value string
}

func (s SubscriptionPlan) String() string { return s.value }

// Equal provides support for the go-cmp package and testing.
func (s SubscriptionPlan) Equal(s2 SubscriptionPlan) bool { return s.value == s2.value }

var (
	PlanFree       = SubscriptionPlan{"free"}
	PlanPro        = SubscriptionPlan{"pro"}
	PlanEnterprise = SubscriptionPlan{"enterprise"}
)

var subscriptionPlans = map[string]SubscriptionPlan{
	"free":       PlanFree,
	"pro":        PlanPro,
	"enterprise": PlanEnterprise,
}

// ParseSubscriptionPlan parses the string value into a SubscriptionPlan.
func ParseSubscriptionPlan(value string) (SubscriptionPlan, error) {
	p, ok := subscriptionPlans[value]
	if !ok {
		return SubscriptionPlan{}, fmt.Errorf("invalid subscription plan %q", value)
	}
	return p, nil
}

// SubscriptionStatus represents the billing state of a subscription.
type SubscriptionStatus struct {
	value string
}

func (s SubscriptionStatus) String() string { return s.value }

// Equal provides support for the go-cmp package and testing.
func (s SubscriptionStatus) Equal(s2 SubscriptionStatus) bool { return s.value == s2.value }

var (
	StatusTrialing = SubscriptionStatus{"trialing"}
	StatusActive   = SubscriptionStatus{"active"}
	StatusPastDue  = SubscriptionStatus{"past_due"}
	StatusCancelled = SubscriptionStatus{"cancelled"}
)

var subscriptionStatuses = map[string]SubscriptionStatus{
	"trialing":  StatusTrialing,
	"active":    StatusActive,
	"past_due":  StatusPastDue,
	"cancelled": StatusCancelled,
}

// ParseSubscriptionStatus parses the string value into a SubscriptionStatus.
func ParseSubscriptionStatus(value string) (SubscriptionStatus, error) {
	s, ok := subscriptionStatuses[value]
	if !ok {
		return SubscriptionStatus{}, fmt.Errorf("invalid subscription status %q", value)
	}
	return s, nil
}

// OrgMemberUser combines membership metadata with the user's profile.
// Used when listing all members of an org — avoids N+1 lookups.
type OrgMemberUser struct {
	MemberID uuid.UUID
	UserID   uuid.UUID
	OrgID    uuid.UUID
	Name     name.Name
	Email    string
	Role     role.Role
	Enabled  bool
	JoinedAt time.Time
}

// UserOrg is returned when listing the orgs a specific user belongs to.
// It embeds the Org and carries the membership role so the frontend can
// know what actions the user is allowed to take within that org.
type UserOrg struct {
	Org
	Role role.Role
}

// Subscription represents a billing subscription attached to an organization.
// Organizations pay — not individual users.
type Subscription struct {
	SubscriptionID uuid.UUID
	OrgID          uuid.UUID
	Plan           SubscriptionPlan
	Status         SubscriptionStatus
	PeriodStart    time.Time
	PeriodEnd      time.Time
	DateCreated    time.Time
	DateUpdated    time.Time
}

// NewSubscription contains the information needed to create a subscription.
type NewSubscription struct {
	OrgID       uuid.UUID
	Plan        SubscriptionPlan
	Status      SubscriptionStatus
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// UpdateSubscription contains the fields that can be changed on a subscription.
type UpdateSubscription struct {
	Plan        *SubscriptionPlan
	Status      *SubscriptionStatus
	PeriodStart *time.Time
	PeriodEnd   *time.Time
}
