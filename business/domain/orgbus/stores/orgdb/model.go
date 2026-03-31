package orgdb

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
)

// orgDB is the database representation of an organization.
type orgDB struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Slug        string    `db:"slug"`
	Enabled     bool      `db:"enabled"`
	DateCreated time.Time `db:"date_created"`
	DateUpdated time.Time `db:"date_updated"`
}

func toDBOrg(bus orgbus.Org) orgDB {
	return orgDB{
		ID:          bus.ID,
		Name:        bus.Name.String(),
		Slug:        bus.Slug,
		Enabled:     bus.Enabled,
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
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Slug        string    `db:"slug"`
	Enabled     bool      `db:"enabled"`
	DateCreated time.Time `db:"date_created"`
	DateUpdated time.Time `db:"date_updated"`
	Role        string    `db:"role"`
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
	MemberID  uuid.UUID `db:"member_id"`
	UserID    uuid.UUID `db:"user_id"`
	OrgID     uuid.UUID `db:"org_id"`
	UserName  string    `db:"user_name"`
	Email     string    `db:"email"`
	Role      string    `db:"role"`
	Enabled   bool      `db:"enabled"`
	JoinedAt  time.Time `db:"joined_at"`
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
		MemberID: db.MemberID,
		UserID:   db.UserID,
		OrgID:    db.OrgID,
		Name:     nme,
		Email:    db.Email,
		Role:     r,
		Enabled:  db.Enabled,
		JoinedAt: db.JoinedAt.In(time.Local),
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
// Subscriptions

// subscriptionDB is the database representation of a subscriptions row.
type subscriptionDB struct {
	SubscriptionID uuid.UUID `db:"subscription_id"`
	OrgID          uuid.UUID `db:"org_id"`
	Plan           string    `db:"plan"`
	Status         string    `db:"status"`
	PeriodStart    time.Time `db:"period_start"`
	PeriodEnd      time.Time `db:"period_end"`
	DateCreated    time.Time `db:"date_created"`
	DateUpdated    time.Time `db:"date_updated"`
}

func toDBSubscription(bus orgbus.Subscription) subscriptionDB {
	return subscriptionDB{
		SubscriptionID: bus.SubscriptionID,
		OrgID:          bus.OrgID,
		Plan:           bus.Plan.String(),
		Status:         bus.Status.String(),
		PeriodStart:    bus.PeriodStart.UTC(),
		PeriodEnd:      bus.PeriodEnd.UTC(),
		DateCreated:    bus.DateCreated.UTC(),
		DateUpdated:    bus.DateUpdated.UTC(),
	}
}

func toBusSubscription(db subscriptionDB) (orgbus.Subscription, error) {
	plan, err := orgbus.ParseSubscriptionPlan(db.Plan)
	if err != nil {
		return orgbus.Subscription{}, fmt.Errorf("parse plan: %w", err)
	}

	status, err := orgbus.ParseSubscriptionStatus(db.Status)
	if err != nil {
		return orgbus.Subscription{}, fmt.Errorf("parse status: %w", err)
	}

	return orgbus.Subscription{
		SubscriptionID: db.SubscriptionID,
		OrgID:          db.OrgID,
		Plan:           plan,
		Status:         status,
		PeriodStart:    db.PeriodStart.In(time.Local),
		PeriodEnd:      db.PeriodEnd.In(time.Local),
		DateCreated:    db.DateCreated.In(time.Local),
		DateUpdated:    db.DateUpdated.In(time.Local),
	}, nil
}
