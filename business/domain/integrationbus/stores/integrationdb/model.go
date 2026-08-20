package integrationdb

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/jmoiron/sqlx/types"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// alertRuleDB is the database representation of an alert rule. project_id is
// scanned as a pointer to tolerate legacy (retired) org-wide rows where it is
// NULL; new rows always carry it.
type alertRuleDB struct {
	ID           uuid.UUID  `db:"id"`
	OrgID        uuid.UUID  `db:"org_id"`
	ProjectID    *uuid.UUID `db:"project_id"`
	ConnectionID uuid.UUID  `db:"connection_id"`
	UserID       *uuid.UUID `db:"user_id"`
	Name         string     `db:"name"`
	Level        string     `db:"level"`
	IsActive     bool       `db:"is_active"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`

	Condition          types.JSONText `db:"condition"`
	DedupWindowSeconds int            `db:"dedup_window_seconds"`
	SnoozeUntil        sql.NullTime   `db:"snooze_until"`
}

func toDBAlertRule(r integrationbus.AlertRule) alertRuleDB {
	projectID := r.ProjectID

	db := alertRuleDB{
		ID:           r.ID,
		OrgID:        r.OrgID,
		ProjectID:    &projectID,
		ConnectionID: r.ConnectionID,
		UserID:       r.UserID,
		Name:         r.Name,
		Level:        r.Level,
		IsActive:     r.IsActive,
		CreatedAt:    r.CreatedAt.UTC(),
		UpdatedAt:    r.UpdatedAt.UTC(),

		DedupWindowSeconds: r.DedupWindowSeconds,
	}

	// A rule always stores a condition. An unset one is written as the level
	// equivalent so nothing can persist a rule that never fires.
	cond := r.Condition
	if cond.Type == "" {
		cond = integrationbus.LevelCondition(r.Level)
	}
	if encoded, err := integrationbus.MarshalCondition(cond); err == nil {
		db.Condition = types.JSONText(encoded)
	}

	if r.SnoozeUntil != nil {
		db.SnoozeUntil = sql.NullTime{Time: r.SnoozeUntil.UTC(), Valid: true}
	}

	return db
}

func toBusAlertRule(db alertRuleDB) integrationbus.AlertRule {
	var projectID uuid.UUID
	if db.ProjectID != nil {
		projectID = *db.ProjectID
	}
	r := integrationbus.AlertRule{
		ID:           db.ID,
		OrgID:        db.OrgID,
		ProjectID:    projectID,
		ConnectionID: db.ConnectionID,
		UserID:       db.UserID,
		Name:         db.Name,
		Level:        db.Level,
		IsActive:     db.IsActive,
		CreatedAt:    db.CreatedAt,
		UpdatedAt:    db.UpdatedAt,

		DedupWindowSeconds: db.DedupWindowSeconds,
	}

	// A row whose condition will not parse falls back to its level rather than
	// becoming a rule that silently never matches anything.
	if len(db.Condition) > 0 {
		if cond, err := integrationbus.ParseCondition([]byte(db.Condition)); err == nil {
			r.Condition = cond
		}
	}
	if r.Condition.Type == "" {
		r.Condition = integrationbus.LevelCondition(db.Level)
	}

	if db.SnoozeUntil.Valid {
		t := db.SnoozeUntil.Time
		r.SnoozeUntil = &t
	}

	return r
}

// integrationDB is the database representation of a configured integration.
// project_id is scanned as a pointer to tolerate legacy org-level rows that
// were never re-homed (see migration v1.25); API-created rows always set it.
type integrationDB struct {
	ID             uuid.UUID  `db:"id"`
	OrgID          uuid.UUID  `db:"org_id"`
	ProjectID      *uuid.UUID `db:"project_id"`
	ProviderID     string     `db:"provider_id"`
	Name           string     `db:"name"`
	CredentialsEnc []byte     `db:"credentials_enc"`
	CredentialsIV  []byte     `db:"credentials_iv"`
	Enabled        bool       `db:"enabled"`
	DateCreated    time.Time  `db:"date_created"`
	DateUpdated    time.Time  `db:"date_updated"`
}

// providerDB is the database representation of an integration provider definition.
type providerDB struct {
	ID          string          `db:"id"`
	Name        string          `db:"name"`
	Icon        string          `db:"icon"`
	Type        string          `db:"type"`
	Description string          `db:"description"`
	FieldsJSON  json.RawMessage `db:"fields"`
}

// providerFieldDB mirrors the JSON structure stored in integration_providers.fields.
type providerFieldDB struct {
	Key         string `json:"k"`
	Label       string `json:"label"`
	Placeholder string `json:"ph,omitempty"`
}

func toDBIntegration(bus integrationbus.Integration, enc, iv []byte) integrationDB {
	projectID := bus.ProjectID
	return integrationDB{
		ID:             bus.ID,
		OrgID:          bus.OrgID,
		ProjectID:      &projectID,
		ProviderID:     bus.ProviderID,
		Name:           bus.Name,
		CredentialsEnc: enc,
		CredentialsIV:  iv,
		Enabled:        bus.Enabled,
		DateCreated:    bus.DateCreated.UTC(),
		DateUpdated:    bus.DateUpdated.UTC(),
	}
}

func toBusIntegration(db integrationDB, creds map[string]string) integrationbus.Integration {
	var projectID uuid.UUID
	if db.ProjectID != nil {
		projectID = *db.ProjectID
	}
	return integrationbus.Integration{
		ID:          db.ID,
		OrgID:       db.OrgID,
		ProjectID:   projectID,
		ProviderID:  db.ProviderID,
		Name:        db.Name,
		Credentials: creds,
		Enabled:     db.Enabled,
		DateCreated: db.DateCreated.In(time.Local),
		DateUpdated: db.DateUpdated.In(time.Local),
	}
}

func toBusProvider(db providerDB) (integrationbus.Provider, error) {
	var raw []providerFieldDB
	if err := json.Unmarshal(db.FieldsJSON, &raw); err != nil {
		return integrationbus.Provider{}, err
	}

	fields := make([]integrationbus.ProviderField, len(raw))
	for i, f := range raw {
		fields[i] = integrationbus.ProviderField{
			Key:         f.Key,
			Label:       f.Label,
			Placeholder: f.Placeholder,
		}
	}

	return integrationbus.Provider{
		ID:          db.ID,
		Name:        db.Name,
		Icon:        db.Icon,
		Type:        db.Type,
		Description: db.Description,
		Fields:      fields,
	}, nil
}
