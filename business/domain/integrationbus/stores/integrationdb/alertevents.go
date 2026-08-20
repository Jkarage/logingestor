package integrationdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
)

// =============================================================================
// Rule loading for evaluation

const ruleEvalColumns = `id, org_id, project_id, connection_id, user_id, name, level,
	is_active, created_at, updated_at, condition, dedup_window_seconds, snooze_until`

// QueryActiveRules returns every active rule for a project.
//
// It deliberately does not pre-filter by level. A match or threshold condition
// cannot be expressed as a level comparison in SQL, so the decision belongs to
// Condition.MatchesLog. Rules per project number in the handful.
func (s *Store) QueryActiveRules(ctx context.Context, projectID uuid.UUID) ([]integrationbus.AlertRule, error) {
	data := struct {
		ProjectID string `db:"project_id"`
	}{ProjectID: projectID.String()}

	const q = `SELECT ` + ruleEvalColumns + `
	FROM alert_rules WHERE project_id = :project_id AND is_active = true`

	var dbs []alertRuleDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusAlertRules(dbs), nil
}

// QueryThresholdRules returns every active threshold rule across all projects,
// which is the working set for the periodic evaluator.
func (s *Store) QueryThresholdRules(ctx context.Context) ([]integrationbus.AlertRule, error) {
	const q = `SELECT ` + ruleEvalColumns + `
	FROM alert_rules
	WHERE is_active = true AND condition->>'type' = 'threshold'`

	var dbs []alertRuleDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, struct{}{}, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusAlertRules(dbs), nil
}

func toBusAlertRules(dbs []alertRuleDB) []integrationbus.AlertRule {
	out := make([]integrationbus.AlertRule, len(dbs))
	for i, db := range dbs {
		out[i] = toBusAlertRule(db)
	}
	return out
}

// =============================================================================
// Alert events

type alertEventDB struct {
	ID             uuid.UUID    `db:"id"`
	RuleID         uuid.UUID    `db:"rule_id"`
	OrgID          uuid.UUID    `db:"org_id"`
	ProjectID      uuid.UUID    `db:"project_id"`
	DedupKey       string       `db:"dedup_key"`
	State          string       `db:"state"`
	Summary        string       `db:"summary"`
	Level          string       `db:"level"`
	MatchCount     int64        `db:"match_count"`
	SampleLogID    *uuid.UUID   `db:"sample_log_id"`
	FirstSeenAt    time.Time    `db:"first_seen_at"`
	LastSeenAt     time.Time    `db:"last_seen_at"`
	LastNotifiedAt sql.NullTime `db:"last_notified_at"`
	ResolvedAt     sql.NullTime `db:"resolved_at"`
	AcknowledgedAt sql.NullTime `db:"acknowledged_at"`
	AcknowledgedBy *uuid.UUID   `db:"acknowledged_by"`
}

func toBusEvent(db alertEventDB) integrationbus.AlertEvent {
	e := integrationbus.AlertEvent{
		ID: db.ID, RuleID: db.RuleID, OrgID: db.OrgID, ProjectID: db.ProjectID,
		DedupKey: db.DedupKey, State: db.State, Summary: db.Summary, Level: db.Level,
		MatchCount: db.MatchCount, SampleLogID: db.SampleLogID,
		FirstSeenAt: db.FirstSeenAt, LastSeenAt: db.LastSeenAt,
		AcknowledgedBy: db.AcknowledgedBy,
	}
	for _, f := range []struct {
		src sql.NullTime
		dst **time.Time
	}{
		{db.LastNotifiedAt, &e.LastNotifiedAt},
		{db.ResolvedAt, &e.ResolvedAt},
		{db.AcknowledgedAt, &e.AcknowledgedAt},
	} {
		if f.src.Valid {
			t := f.src.Time
			*f.dst = &t
		}
	}
	return e
}

const eventColumns = `id, rule_id, org_id, project_id, dedup_key, state, summary, level,
	match_count, sample_log_id, first_seen_at, last_seen_at, last_notified_at,
	resolved_at, acknowledged_at, acknowledged_by`

// QueryOpenEvent returns the unresolved event for a rule and dedup key. The
// partial unique index guarantees there is at most one.
func (s *Store) QueryOpenEvent(ctx context.Context, ruleID uuid.UUID, dedupKey string) (integrationbus.AlertEvent, error) {
	data := struct {
		RuleID   string `db:"rule_id"`
		DedupKey string `db:"dedup_key"`
	}{RuleID: ruleID.String(), DedupKey: dedupKey}

	const q = `SELECT ` + eventColumns + `
	FROM alert_events
	WHERE rule_id = :rule_id AND dedup_key = :dedup_key AND state <> 'resolved'`

	// Finding nothing is the common case — most firings are the first of their
	// kind — so the miss is not logged. raise() decides what it means.
	var db alertEventDB
	if err := sqldb.NamedQueryStructAllowNotFound(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return integrationbus.AlertEvent{}, fmt.Errorf("db: %w", integrationbus.ErrEventNotFound)
		}
		return integrationbus.AlertEvent{}, fmt.Errorf("db: %w", err)
	}

	return toBusEvent(db), nil
}

// RecordFiring opens an alert or folds a repeat into the open one.
//
// The upsert targets the partial unique index on (rule_id, dedup_key) where the
// state is not resolved, so two concurrent ingest batches cannot create two open
// alerts for the same thing. A repeat advances last_seen_at and adds to the
// match count without disturbing when it was first seen or whether it has been
// acknowledged.
func (s *Store) RecordFiring(ctx context.Context, e integrationbus.AlertEvent) (integrationbus.AlertEvent, error) {
	data := struct {
		RuleID      string     `db:"rule_id"`
		OrgID       string     `db:"org_id"`
		ProjectID   string     `db:"project_id"`
		DedupKey    string     `db:"dedup_key"`
		Summary     string     `db:"summary"`
		Level       string     `db:"level"`
		MatchCount  int64      `db:"match_count"`
		SampleLogID *uuid.UUID `db:"sample_log_id"`
		At          time.Time  `db:"at"`
	}{
		RuleID: e.RuleID.String(), OrgID: e.OrgID.String(), ProjectID: e.ProjectID.String(),
		DedupKey: e.DedupKey, Summary: e.Summary, Level: e.Level,
		MatchCount: e.MatchCount, SampleLogID: e.SampleLogID, At: e.LastSeenAt.UTC(),
	}

	const q = `
	INSERT INTO alert_events
		(rule_id, org_id, project_id, dedup_key, state, summary, level, match_count,
		 sample_log_id, first_seen_at, last_seen_at)
	VALUES
		(:rule_id, :org_id, :project_id, :dedup_key, 'firing', :summary, :level, :match_count,
		 :sample_log_id, :at, :at)
	ON CONFLICT (rule_id, dedup_key) WHERE state <> 'resolved'
	DO UPDATE SET
		last_seen_at = EXCLUDED.last_seen_at,
		match_count  = alert_events.match_count + EXCLUDED.match_count,
		summary      = EXCLUDED.summary,
		level        = EXCLUDED.level
	RETURNING ` + eventColumns

	var db alertEventDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		return integrationbus.AlertEvent{}, fmt.Errorf("namedquerystruct: %w", err)
	}

	return toBusEvent(db), nil
}

// MarkNotified records that an alert was delivered, which is what the dedup
// window is measured from.
func (s *Store) MarkNotified(ctx context.Context, eventID uuid.UUID, at time.Time) error {
	data := struct {
		ID string    `db:"id"`
		At time.Time `db:"at"`
	}{ID: eventID.String(), At: at.UTC()}

	const q = `UPDATE alert_events SET last_notified_at = :at WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// AcknowledgeEvent marks an open alert as owned by a user.
func (s *Store) AcknowledgeEvent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error {
	data := struct {
		ID string    `db:"id"`
		By string    `db:"by"`
		At time.Time `db:"at"`
	}{ID: eventID.String(), By: userID.String(), At: at.UTC()}

	const q = `
	UPDATE alert_events
	SET state = 'acknowledged', acknowledged_at = :at, acknowledged_by = :by
	WHERE id = :id AND state = 'firing'`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// ResolveEvent closes an alert, freeing its dedup key for a future firing.
func (s *Store) ResolveEvent(ctx context.Context, eventID uuid.UUID, at time.Time) error {
	data := struct {
		ID string    `db:"id"`
		At time.Time `db:"at"`
	}{ID: eventID.String(), At: at.UTC()}

	const q = `
	UPDATE alert_events
	SET state = 'resolved', resolved_at = :at
	WHERE id = :id AND state <> 'resolved'`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryEventByID returns one alert event.
func (s *Store) QueryEventByID(ctx context.Context, id uuid.UUID) (integrationbus.AlertEvent, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `SELECT ` + eventColumns + ` FROM alert_events WHERE id = :id`

	var db alertEventDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return integrationbus.AlertEvent{}, fmt.Errorf("db: %w", integrationbus.ErrEventNotFound)
		}
		return integrationbus.AlertEvent{}, fmt.Errorf("db: %w", err)
	}

	return toBusEvent(db), nil
}

// QueryEvents returns alert history for an org, newest first.
func (s *Store) QueryEvents(ctx context.Context, f integrationbus.AlertEventFilter) ([]integrationbus.AlertEvent, error) {
	data := map[string]any{
		"org_id": f.OrgID.String(),
		"limit":  f.Limit,
	}

	q := `SELECT ` + eventColumns + ` FROM alert_events WHERE org_id = :org_id`

	if f.ProjectID != nil {
		data["project_id"] = f.ProjectID.String()
		q += ` AND project_id = :project_id`
	}
	if f.State != nil {
		data["state"] = *f.State
		q += ` AND state = :state`
	}
	if f.RuleID != nil {
		data["rule_id"] = f.RuleID.String()
		q += ` AND rule_id = :rule_id`
	}
	if f.Since != nil {
		data["since"] = f.Since.UTC()
		q += ` AND last_seen_at >= :since`
	}

	q += ` ORDER BY last_seen_at DESC, id DESC LIMIT :limit`

	var dbs []alertEventDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]integrationbus.AlertEvent, len(dbs))
	for i, db := range dbs {
		out[i] = toBusEvent(db)
	}

	return out, nil
}

// =============================================================================
// Maintenance windows

type maintenanceDB struct {
	ID          uuid.UUID  `db:"id"`
	OrgID       uuid.UUID  `db:"org_id"`
	ProjectID   *uuid.UUID `db:"project_id"`
	Reason      string     `db:"reason"`
	StartsAt    time.Time  `db:"starts_at"`
	EndsAt      time.Time  `db:"ends_at"`
	CreatedBy   *uuid.UUID `db:"created_by"`
	DateCreated time.Time  `db:"date_created"`
}

const maintenanceColumns = `id, org_id, project_id, reason, starts_at, ends_at, created_by, date_created`

func toBusMaintenance(db maintenanceDB) integrationbus.MaintenanceWindow {
	return integrationbus.MaintenanceWindow{
		ID: db.ID, OrgID: db.OrgID, ProjectID: db.ProjectID, Reason: db.Reason,
		StartsAt: db.StartsAt, EndsAt: db.EndsAt, CreatedBy: db.CreatedBy,
		DateCreated: db.DateCreated,
	}
}

// CreateMaintenance stores a maintenance window.
func (s *Store) CreateMaintenance(ctx context.Context, w integrationbus.MaintenanceWindow) error {
	data := maintenanceDB{
		ID: w.ID, OrgID: w.OrgID, ProjectID: w.ProjectID, Reason: w.Reason,
		StartsAt: w.StartsAt.UTC(), EndsAt: w.EndsAt.UTC(), CreatedBy: w.CreatedBy,
		DateCreated: w.DateCreated.UTC(),
	}

	const q = `
	INSERT INTO alert_maintenance_windows
		(id, org_id, project_id, reason, starts_at, ends_at, created_by, date_created)
	VALUES
		(:id, :org_id, :project_id, :reason, :starts_at, :ends_at, :created_by, :date_created)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// DeleteMaintenance removes a maintenance window.
func (s *Store) DeleteMaintenance(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, `DELETE FROM alert_maintenance_windows WHERE id = :id`, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryMaintenanceByOrg returns an org's windows, soonest first.
func (s *Store) QueryMaintenanceByOrg(ctx context.Context, orgID uuid.UUID) ([]integrationbus.MaintenanceWindow, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `SELECT ` + maintenanceColumns + `
	FROM alert_maintenance_windows WHERE org_id = :org_id ORDER BY starts_at DESC`

	var dbs []maintenanceDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]integrationbus.MaintenanceWindow, len(dbs))
	for i, db := range dbs {
		out[i] = toBusMaintenance(db)
	}

	return out, nil
}

// QueryActiveMaintenance returns the windows covering an instant. Only these
// matter to a delivery decision, so the filtering is done in SQL.
func (s *Store) QueryActiveMaintenance(ctx context.Context, orgID uuid.UUID, at time.Time) ([]integrationbus.MaintenanceWindow, error) {
	data := struct {
		OrgID string    `db:"org_id"`
		At    time.Time `db:"at"`
	}{OrgID: orgID.String(), At: at.UTC()}

	const q = `SELECT ` + maintenanceColumns + `
	FROM alert_maintenance_windows
	WHERE org_id = :org_id AND starts_at <= :at AND ends_at > :at`

	var dbs []maintenanceDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	out := make([]integrationbus.MaintenanceWindow, len(dbs))
	for i, db := range dbs {
		out[i] = toBusMaintenance(db)
	}

	return out, nil
}
