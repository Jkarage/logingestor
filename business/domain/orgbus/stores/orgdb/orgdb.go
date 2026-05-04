// Package orgdb contains organization related CRUD functionality.
package orgdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for organization database access.
type Store struct {
	log *logger.Logger
	db  sqlx.ExtContext
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{
		log: log,
		db:  db,
	}
}

// NewWithTx constructs a new Store value replacing the sqlx DB
// value with a sqlx DB value that is currently inside a transaction.
func (s *Store) NewWithTx(tx sqldb.CommitRollbacker) (orgbus.Storer, error) {
	ec, err := sqldb.GetExtContext(tx)
	if err != nil {
		return nil, err
	}

	return &Store{
		log: s.log,
		db:  ec,
	}, nil
}

// =============================================================================
// Org CRUD

// Create inserts a new organization into the database.
func (s *Store) Create(ctx context.Context, org orgbus.Org) error {
	const q = `
	INSERT INTO organizations
		(id, name, slug, enabled, date_created, date_updated)
	VALUES
		(:id, :name, :slug, :enabled, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBOrg(org)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", orgbus.ErrUniqueSlug)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update replaces an organization in the database.
func (s *Store) Update(ctx context.Context, org orgbus.Org) error {
	const q = `
	UPDATE organizations
	SET
		name         = :name,
		slug         = :slug,
		enabled      = :enabled,
		date_updated = :date_updated
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBOrg(org)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", orgbus.ErrUniqueSlug)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes an organization from the database.
// Cascades to org_members and subscriptions via FK constraints.
func (s *Store) Delete(ctx context.Context, org orgbus.Org) error {
	const q = `
	DELETE FROM organizations
	WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBOrg(org)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Query retrieves a list of organizations from the database.
func (s *Store) Query(ctx context.Context, filter orgbus.QueryFilter, orderBy order.By, page page.Page) ([]orgbus.Org, error) {
	data := map[string]any{
		"offset":        (page.Number() - 1) * page.RowsPerPage(),
		"rows_per_page": page.RowsPerPage(),
	}

	const q = `
	SELECT
		id, name, slug, enabled, date_created, date_updated
	FROM
		organizations`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	orderByClause, err := orderByClause(orderBy)
	if err != nil {
		return nil, err
	}

	buf.WriteString(orderByClause)
	buf.WriteString(" OFFSET :offset ROWS FETCH NEXT :rows_per_page ROWS ONLY")

	var dbOrgs []orgDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &dbOrgs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusOrgs(dbOrgs)
}

// Count returns the total number of organizations matching the filter.
func (s *Store) Count(ctx context.Context, filter orgbus.QueryFilter) (int, error) {
	data := map[string]any{}

	const q = `
	SELECT count(1)
	FROM organizations`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	var count struct {
		Count int `db:"count"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, buf.String(), data, &count); err != nil {
		return 0, fmt.Errorf("db: %w", err)
	}

	return count.Count, nil
}

// QueryByID gets the specified organization from the database.
func (s *Store) QueryByID(ctx context.Context, orgID uuid.UUID) (orgbus.Org, error) {
	data := struct {
		ID string `db:"id"`
	}{
		ID: orgID.String(),
	}

	const q = `
	SELECT
		id, name, slug, enabled, date_created, date_updated
	FROM
		organizations
	WHERE
		id = :id`

	var dbOrg orgDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbOrg); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return orgbus.Org{}, fmt.Errorf("db: %w", orgbus.ErrNotFound)
		}
		return orgbus.Org{}, fmt.Errorf("db: %w", err)
	}

	return toBusOrg(dbOrg)
}

// QueryBySlug gets the specified organization by its slug.
func (s *Store) QueryBySlug(ctx context.Context, slug string) (orgbus.Org, error) {
	data := struct {
		Slug string `db:"slug"`
	}{
		Slug: slug,
	}

	const q = `
	SELECT
		id, name, slug, enabled, date_created, date_updated
	FROM
		organizations
	WHERE
		slug = :slug`

	var dbOrg orgDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbOrg); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return orgbus.Org{}, fmt.Errorf("db: %w", orgbus.ErrNotFound)
		}
		return orgbus.Org{}, fmt.Errorf("db: %w", err)
	}

	return toBusOrg(dbOrg)
}

// QueryByUserID returns every org the given user is a member of, along with
// their role in that org. Used by the frontend to list available workspaces.
func (s *Store) QueryByUserID(ctx context.Context, userID uuid.UUID) ([]orgbus.UserOrg, error) {
	data := struct {
		UserID string `db:"user_id"`
	}{
		UserID: userID.String(),
	}

	const q = `
	SELECT
		o.id, o.name, o.slug, o.enabled, o.date_created, o.date_updated,
		m.role
	FROM
		organizations o
	JOIN
		org_members m ON m.org_id = o.id
	WHERE
		m.user_id = :user_id
	ORDER BY
		o.date_created ASC`

	var dbOrgs []userOrgDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbOrgs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusUserOrgs(dbOrgs)
}

// UpdateEnabled sets the enabled flag on an organization.
func (s *Store) UpdateEnabled(ctx context.Context, orgID uuid.UUID, enabled bool) error {
	const q = `
	UPDATE organizations
	SET enabled = :enabled
	WHERE id = :id`

	data := struct {
		ID      string `db:"id"`
		Enabled bool   `db:"enabled"`
	}{
		ID:      orgID.String(),
		Enabled: enabled,
	}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// =============================================================================
// Membership

// AddMember inserts a new row into org_members.
func (s *Store) AddMember(ctx context.Context, member orgbus.OrgMember) error {
	const q = `
	INSERT INTO org_members
		(member_id, org_id, user_id, role, joined_at)
	VALUES
		(:member_id, :org_id, :user_id, :role, :joined_at)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBOrgMember(member)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", orgbus.ErrMemberExists)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// RemoveMember deletes a membership row by member_id.
func (s *Store) RemoveMember(ctx context.Context, memberID uuid.UUID) error {
	const q = `
	DELETE FROM org_members
	WHERE member_id = :member_id`

	data := struct {
		MemberID string `db:"member_id"`
	}{
		MemberID: memberID.String(),
	}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UpdateMemberRole changes the role on a membership row.
func (s *Store) UpdateMemberRole(ctx context.Context, memberID uuid.UUID, r role.Role) error {
	const q = `
	UPDATE org_members
	SET role = :role
	WHERE member_id = :member_id`

	data := struct {
		MemberID string `db:"member_id"`
		Role     string `db:"role"`
	}{
		MemberID: memberID.String(),
		Role:     r.String(),
	}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryMembers returns all membership rows for a given org.
func (s *Store) QueryMembers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMember, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{
		OrgID: orgID.String(),
	}

	const q = `
	SELECT
		member_id, org_id, user_id, role, joined_at
	FROM
		org_members
	WHERE
		org_id = :org_id
	ORDER BY
		joined_at ASC`

	var dbMembers []orgMemberDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbMembers); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusOrgMembers(dbMembers)
}

// QueryMembersWithUsers returns all members of an org joined with their user profile.
func (s *Store) QueryMembersWithUsers(ctx context.Context, orgID uuid.UUID) ([]orgbus.OrgMemberUser, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{
		OrgID: orgID.String(),
	}

	const q = `
	SELECT
		m.member_id,
		m.user_id,
		m.org_id,
		u.name  AS user_name,
		u.email,
		m.role,
		u.enabled,
		m.joined_at,
		(
			SELECT COUNT(*)
			FROM user_project_access upa
			JOIN projects p ON p.id = upa.project_id
			WHERE upa.user_id = m.user_id
			  AND p.org_id = m.org_id
		) AS project_count
	FROM
		org_members m
	JOIN
		users u ON u.id = m.user_id
	WHERE
		m.org_id = :org_id
	ORDER BY
		m.joined_at ASC`

	var dbMembers []orgMemberUserDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbMembers); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusOrgMemberUsers(dbMembers)
}

// QueryMemberByID returns a single membership row.
func (s *Store) QueryMemberByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMember, error) {
	data := struct {
		MemberID string `db:"member_id"`
	}{
		MemberID: memberID.String(),
	}

	const q = `
	SELECT
		member_id, org_id, user_id, role, joined_at
	FROM
		org_members
	WHERE
		member_id = :member_id`

	var dbMember orgMemberDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbMember); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return orgbus.OrgMember{}, fmt.Errorf("db: %w", orgbus.ErrMemberNotFound)
		}
		return orgbus.OrgMember{}, fmt.Errorf("db: %w", err)
	}

	return toBusOrgMember(dbMember)
}

// QueryMemberWithUserByID returns a single membership row joined with the user profile.
func (s *Store) QueryMemberWithUserByID(ctx context.Context, memberID uuid.UUID) (orgbus.OrgMemberUser, error) {
	data := struct {
		MemberID string `db:"member_id"`
	}{
		MemberID: memberID.String(),
	}

	const q = `
	SELECT
		m.member_id,
		m.user_id,
		m.org_id,
		u.name  AS user_name,
		u.email,
		m.role,
		u.enabled,
		m.joined_at,
		(
			SELECT COUNT(*)
			FROM user_project_access upa
			JOIN projects p ON p.id = upa.project_id
			WHERE upa.user_id = m.user_id
			  AND p.org_id = m.org_id
		) AS project_count
	FROM
		org_members m
	JOIN
		users u ON u.id = m.user_id
	WHERE
		m.member_id = :member_id`

	var dbMember orgMemberUserDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbMember); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return orgbus.OrgMemberUser{}, fmt.Errorf("db: %w", orgbus.ErrMemberNotFound)
		}
		return orgbus.OrgMemberUser{}, fmt.Errorf("db: %w", err)
	}

	return toBusOrgMemberUser(dbMember)
}

// =============================================================================
// Subscriptions

// CreateSubscription inserts a new subscription row.
func (s *Store) CreateSubscription(ctx context.Context, sub orgbus.Subscription) error {
	const q = `
	INSERT INTO subscriptions
		(subscription_id, org_id, plan, status, period_start, period_end, date_created, date_updated)
	VALUES
		(:subscription_id, :org_id, :plan, :status, :period_start, :period_end, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBSubscription(sub)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UpdateSubscription replaces billing fields on an existing subscription row.
func (s *Store) UpdateSubscription(ctx context.Context, sub orgbus.Subscription) error {
	const q = `
	UPDATE subscriptions
	SET
		plan         = :plan,
		status       = :status,
		period_start = :period_start,
		period_end   = :period_end,
		date_updated = :date_updated
	WHERE
		subscription_id = :subscription_id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBSubscription(sub)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QuerySubscription returns the subscription for a given org.
func (s *Store) QuerySubscription(ctx context.Context, orgID uuid.UUID) (orgbus.Subscription, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{
		OrgID: orgID.String(),
	}

	const q = `
	SELECT
		subscription_id, org_id, plan, status, period_start, period_end, date_created, date_updated
	FROM
		subscriptions
	WHERE
		org_id = :org_id
	ORDER BY
		date_created DESC
	FETCH FIRST 1 ROWS ONLY`

	var dbSub subscriptionDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbSub); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return orgbus.Subscription{}, fmt.Errorf("db: %w", orgbus.ErrNotFound)
		}
		return orgbus.Subscription{}, fmt.Errorf("db: %w", err)
	}

	return toBusSubscription(dbSub)
}
