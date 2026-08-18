package orgdb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
)

// orgDB is the database representation of an organization.
type orgDB struct {
	ID          uuid.UUID  `db:"id"`
	Name        string     `db:"name"`
	Slug        string     `db:"slug"`
	Enabled     bool       `db:"enabled"`
	CreatedBy   *uuid.UUID `db:"created_by"`
	DateCreated time.Time  `db:"date_created"`
	DateUpdated time.Time  `db:"date_updated"`
}

func toDBOrg(bus orgbus.Org) orgDB {
	return orgDB{
		ID:          bus.ID,
		Name:        bus.Name.String(),
		Slug:        bus.Slug,
		Enabled:     bus.Enabled,
		CreatedBy:   bus.CreatedBy,
		DateCreated: bus.DateCreated.UTC(),
		DateUpdated: bus.DateUpdated.UTC(),
	}
}

func toBusOrg(db orgDB) (orgbus.Org, error) {
	nme, err := name.Parse(db.Name)
	if err != nil {
		return orgbus.Org{}, fmt.Errorf("parse name: %w", err)
	}

	return orgbus.Org{
		ID:          db.ID,
		Name:        nme,
		Slug:        db.Slug,
		Enabled:     db.Enabled,
		CreatedBy:   db.CreatedBy,
		DateCreated: db.DateCreated.In(time.Local),
		DateUpdated: db.DateUpdated.In(time.Local),
	}, nil
}

func toBusOrgs(dbs []orgDB) ([]orgbus.Org, error) {
	orgs := make([]orgbus.Org, len(dbs))
	for i, db := range dbs {
		var err error
		orgs[i], err = toBusOrg(db)
		if err != nil {
			return nil, err
		}
	}
	return orgs, nil
}

// userOrgDB is used for the JOIN query that fetches orgs a user belongs to.
type userOrgDB struct {
	ID          uuid.UUID  `db:"id"`
	Name        string     `db:"name"`
	Slug        string     `db:"slug"`
	Enabled     bool       `db:"enabled"`
	CreatedBy   *uuid.UUID `db:"created_by"`
	DateCreated time.Time  `db:"date_created"`
	DateUpdated time.Time  `db:"date_updated"`
	Role        string     `db:"role"`
}

func toBusUserOrg(db userOrgDB) (orgbus.UserOrg, error) {
	nme, err := name.Parse(db.Name)
	if err != nil {
		return orgbus.UserOrg{}, fmt.Errorf("parse name: %w", err)
	}

	r, err := role.Parse(db.Role)
	if err != nil {
		return orgbus.UserOrg{}, fmt.Errorf("parse role: %w", err)
	}

	return orgbus.UserOrg{
		Org: orgbus.Org{
			ID:          db.ID,
			Name:        nme,
			Slug:        db.Slug,
			Enabled:     db.Enabled,
			CreatedBy:   db.CreatedBy,
			DateCreated: db.DateCreated.In(time.Local),
			DateUpdated: db.DateUpdated.In(time.Local),
		},
		Role: r,
	}, nil
}

func toBusUserOrgs(dbs []userOrgDB) ([]orgbus.UserOrg, error) {
	orgs := make([]orgbus.UserOrg, len(dbs))
	for i, db := range dbs {
		var err error
		orgs[i], err = toBusUserOrg(db)
		if err != nil {
			return nil, err
		}
	}
	return orgs, nil
}

// =============================================================================
// Membership

// orgMemberDB is the database representation of an org_members row.
type orgMemberDB struct {
	MemberID uuid.UUID `db:"member_id"`
	OrgID    uuid.UUID `db:"org_id"`
	UserID   uuid.UUID `db:"user_id"`
	Role     string    `db:"role"`
	JoinedAt time.Time `db:"joined_at"`
}

func toDBOrgMember(bus orgbus.OrgMember) orgMemberDB {
	return orgMemberDB{
		MemberID: bus.MemberID,
		OrgID:    bus.OrgID,
		UserID:   bus.UserID,
		Role:     bus.Role.String(),
		JoinedAt: bus.JoinedAt.UTC(),
	}
}

func toBusOrgMember(db orgMemberDB) (orgbus.OrgMember, error) {
	r, err := role.Parse(db.Role)
	if err != nil {
		return orgbus.OrgMember{}, fmt.Errorf("parse role: %w", err)
	}

	return orgbus.OrgMember{
		MemberID: db.MemberID,
		OrgID:    db.OrgID,
		UserID:   db.UserID,
		Role:     r,
		JoinedAt: db.JoinedAt.In(time.Local),
	}, nil
}

func toBusOrgMembers(dbs []orgMemberDB) ([]orgbus.OrgMember, error) {
	members := make([]orgbus.OrgMember, len(dbs))
	for i, db := range dbs {
		var err error
		members[i], err = toBusOrgMember(db)
		if err != nil {
			return nil, err
		}
	}
	return members, nil
}

// orgMemberUserDB is the result of the JOIN between org_members and users.
type orgMemberUserDB struct {
	MemberID     uuid.UUID `db:"member_id"`
	UserID       uuid.UUID `db:"user_id"`
	OrgID        uuid.UUID `db:"org_id"`
	UserName     string    `db:"user_name"`
	Email        string    `db:"email"`
	Role         string    `db:"role"`
	Enabled      bool      `db:"enabled"`
	JoinedAt     time.Time `db:"joined_at"`
	ProjectCount int       `db:"project_count"`
}

func toBusOrgMemberUser(db orgMemberUserDB) (orgbus.OrgMemberUser, error) {
	nme, err := name.Parse(db.UserName)
	if err != nil {
		return orgbus.OrgMemberUser{}, fmt.Errorf("parse name: %w", err)
	}

	r, err := role.Parse(db.Role)
	if err != nil {
		return orgbus.OrgMemberUser{}, fmt.Errorf("parse role: %w", err)
	}

	return orgbus.OrgMemberUser{
		MemberID:     db.MemberID,
		UserID:       db.UserID,
		OrgID:        db.OrgID,
		Name:         nme,
		Email:        db.Email,
		Role:         r,
		Enabled:      db.Enabled,
		JoinedAt:     db.JoinedAt.In(time.Local),
		ProjectCount: db.ProjectCount,
	}, nil
}

func toBusOrgMemberUsers(dbs []orgMemberUserDB) ([]orgbus.OrgMemberUser, error) {
	members := make([]orgbus.OrgMemberUser, len(dbs))
	for i, db := range dbs {
		var err error
		members[i], err = toBusOrgMemberUser(db)
		if err != nil {
			return nil, err
		}
	}
	return members, nil
}

// =============================================================================
// Plans

// planDB is the database representation of a plans row.
type planDB struct {
	ID            uuid.UUID `db:"id"`
	Slug          string    `db:"slug"`
	Name          string    `db:"name"`
	PriceCents    int       `db:"price_cents"`
	Interval      string    `db:"interval"`
	StripePriceID *string   `db:"stripe_price_id"`
	Features      []byte    `db:"features"`
	IsActive      bool      `db:"is_active"`
	CreatedAt     time.Time `db:"created_at"`
}

func toBusPlan(db planDB) (orgbus.Plan, error) {
	slug, err := orgbus.ParseSubscriptionPlan(db.Slug)
	if err != nil {
		return orgbus.Plan{}, fmt.Errorf("parse plan slug: %w", err)
	}

	var features orgbus.PlanFeatures
	if err := json.Unmarshal(db.Features, &features); err != nil {
		return orgbus.Plan{}, fmt.Errorf("unmarshal plan features: %w", err)
	}

	priceID := ""
	if db.StripePriceID != nil {
		priceID = *db.StripePriceID
	}

	return orgbus.Plan{
		PlanID:        db.ID,
		Slug:          slug,
		Name:          db.Name,
		PriceCents:    db.PriceCents,
		Interval:      db.Interval,
		StripePriceID: priceID,
		Features:      features,
		IsActive:      db.IsActive,
		CreatedAt:     db.CreatedAt.In(time.Local),
	}, nil
}

func toBusPlans(dbs []planDB) ([]orgbus.Plan, error) {
	plans := make([]orgbus.Plan, len(dbs))
	for i, db := range dbs {
		var err error
		plans[i], err = toBusPlan(db)
		if err != nil {
			return nil, err
		}
	}
	return plans, nil
}

// =============================================================================
// Subscriptions

// subscriptionDB is the database representation of a subscriptions row joined
// with its plan.
type subscriptionDB struct {
	SubscriptionID       uuid.UUID  `db:"subscription_id"`
	OrgID                uuid.UUID  `db:"org_id"`
	PlanID               uuid.UUID  `db:"plan_id"`
	PlanSlug             string     `db:"plan_slug"`
	PlanName             string     `db:"plan_name"`
	PlanFeatures         []byte     `db:"plan_features"`
	Status               string     `db:"status"`
	StripeCustomerID     *string    `db:"stripe_customer_id"`
	StripeSubscriptionID *string    `db:"stripe_subscription_id"`
	CancelAtPeriodEnd    bool       `db:"cancel_at_period_end"`
	CancelledAt          *time.Time `db:"cancelled_at"`
	PeriodStart          *time.Time `db:"period_start"`
	PeriodEnd            *time.Time `db:"period_end"`
	DateCreated          time.Time  `db:"date_created"`
	DateUpdated          time.Time  `db:"date_updated"`
}

// subscriptionWriteDB is used for INSERT/UPDATE where we don't need plan JOIN fields.
type subscriptionWriteDB struct {
	SubscriptionID       uuid.UUID  `db:"subscription_id"`
	OrgID                uuid.UUID  `db:"org_id"`
	PlanID               uuid.UUID  `db:"plan_id"`
	Status               string     `db:"status"`
	StripeCustomerID     *string    `db:"stripe_customer_id"`
	StripeSubscriptionID *string    `db:"stripe_subscription_id"`
	CancelAtPeriodEnd    bool       `db:"cancel_at_period_end"`
	CancelledAt          *time.Time `db:"cancelled_at"`
	PeriodStart          *time.Time `db:"period_start"`
	PeriodEnd            *time.Time `db:"period_end"`
	DateCreated          time.Time  `db:"date_created"`
	DateUpdated          time.Time  `db:"date_updated"`
}

func toWriteDBSubscription(bus orgbus.Subscription) subscriptionWriteDB {
	var custID *string
	if bus.StripeCustomerID != "" {
		s := bus.StripeCustomerID
		custID = &s
	}
	var subID *string
	if bus.StripeSubscriptionID != "" {
		s := bus.StripeSubscriptionID
		subID = &s
	}

	return subscriptionWriteDB{
		SubscriptionID:       bus.SubscriptionID,
		OrgID:                bus.OrgID,
		PlanID:               bus.PlanID,
		Status:               bus.Status.String(),
		StripeCustomerID:     custID,
		StripeSubscriptionID: subID,
		CancelAtPeriodEnd:    bus.CancelAtPeriodEnd,
		CancelledAt:          bus.CancelledAt,
		PeriodStart:          bus.PeriodStart,
		PeriodEnd:            bus.PeriodEnd,
		DateCreated:          bus.DateCreated.UTC(),
		DateUpdated:          bus.DateUpdated.UTC(),
	}
}

func toBusSubscription(db subscriptionDB) (orgbus.Subscription, error) {
	plan, err := orgbus.ParseSubscriptionPlan(db.PlanSlug)
	if err != nil {
		return orgbus.Subscription{}, fmt.Errorf("parse plan: %w", err)
	}

	status, err := orgbus.ParseSubscriptionStatus(db.Status)
	if err != nil {
		return orgbus.Subscription{}, fmt.Errorf("parse status: %w", err)
	}

	var features orgbus.PlanFeatures
	if err := json.Unmarshal(db.PlanFeatures, &features); err != nil {
		return orgbus.Subscription{}, fmt.Errorf("unmarshal features: %w", err)
	}

	custID := ""
	if db.StripeCustomerID != nil {
		custID = *db.StripeCustomerID
	}
	subID := ""
	if db.StripeSubscriptionID != nil {
		subID = *db.StripeSubscriptionID
	}

	var periodStart, periodEnd *time.Time
	if db.PeriodStart != nil {
		t := db.PeriodStart.In(time.Local)
		periodStart = &t
	}
	if db.PeriodEnd != nil {
		t := db.PeriodEnd.In(time.Local)
		periodEnd = &t
	}

	return orgbus.Subscription{
		SubscriptionID:       db.SubscriptionID,
		OrgID:                db.OrgID,
		PlanID:               db.PlanID,
		Plan:                 plan,
		PlanName:             db.PlanName,
		PlanFeatures:         features,
		Status:               status,
		StripeCustomerID:     custID,
		StripeSubscriptionID: subID,
		CancelAtPeriodEnd:    db.CancelAtPeriodEnd,
		CancelledAt:          db.CancelledAt,
		PeriodStart:          periodStart,
		PeriodEnd:            periodEnd,
		DateCreated:          db.DateCreated.In(time.Local),
		DateUpdated:          db.DateUpdated.In(time.Local),
	}, nil
}
