// Package integrationdb contains integration related CRUD functionality.
package integrationdb

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for integration database access.
type Store struct {
	log *logger.Logger
	db  sqlx.ExtContext
	key []byte // 32-byte AES-256-GCM key
}

// NewStore constructs the API for data access.
// key must be exactly 32 bytes (AES-256).
func NewStore(log *logger.Logger, db *sqlx.DB, key []byte) *Store {
	return &Store{
		log: log,
		db:  db,
		key: key,
	}
}

// =============================================================================
// Encryption helpers

func (s *Store) encryptCreds(creds map[string]string) (enc, iv []byte, err error) {
	data, err := json.Marshal(creds)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal: %w", err)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("new gcm: %w", err)
	}

	iv = make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}

	enc = gcm.Seal(nil, iv, data, nil)
	return enc, iv, nil
}

func (s *Store) decryptCreds(enc, iv []byte) (map[string]string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	plain, err := gcm.Open(nil, iv, enc, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}

	var creds map[string]string
	if err := json.Unmarshal(plain, &creds); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return creds, nil
}

// =============================================================================
// CRUD

// Create inserts a new integration into the database.
func (s *Store) Create(ctx context.Context, i integrationbus.Integration) error {
	enc, iv, err := s.encryptCreds(i.Credentials)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	const q = `
	INSERT INTO integrations
		(id, org_id, provider_id, name, credentials_enc, credentials_iv, enabled, date_created, date_updated)
	VALUES
		(:id, :org_id, :provider_id, :name, :credentials_enc, :credentials_iv, :enabled, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBIntegration(i, enc, iv)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", integrationbus.ErrDuplicateName)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update replaces an integration's mutable fields in the database.
func (s *Store) Update(ctx context.Context, i integrationbus.Integration) error {
	enc, iv, err := s.encryptCreds(i.Credentials)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	const q = `
	UPDATE integrations
	SET
		name            = :name,
		credentials_enc = :credentials_enc,
		credentials_iv  = :credentials_iv,
		enabled         = :enabled,
		date_updated    = :date_updated
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBIntegration(i, enc, iv)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes an integration from the database.
func (s *Store) Delete(ctx context.Context, i integrationbus.Integration) error {
	data := struct {
		ID uuid.UUID `db:"id"`
	}{ID: i.ID}

	const q = `DELETE FROM integrations WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryByID gets the specified integration from the database.
func (s *Store) QueryByID(ctx context.Context, id uuid.UUID) (integrationbus.Integration, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `
	SELECT
		id, org_id, provider_id, name, credentials_enc, credentials_iv, enabled, date_created, date_updated
	FROM
		integrations
	WHERE
		id = :id`

	var db integrationDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return integrationbus.Integration{}, fmt.Errorf("db: %w", integrationbus.ErrNotFound)
		}
		return integrationbus.Integration{}, fmt.Errorf("db: %w", err)
	}

	creds, err := s.decryptCreds(db.CredentialsEnc, db.CredentialsIV)
	if err != nil {
		return integrationbus.Integration{}, fmt.Errorf("decrypt: %w", err)
	}

	return toBusIntegration(db, creds), nil
}

// QueryByOrg returns all integrations configured for an org, ordered by creation date.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]integrationbus.Integration, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT
		id, org_id, provider_id, name, credentials_enc, credentials_iv, enabled, date_created, date_updated
	FROM
		integrations
	WHERE
		org_id = :org_id
	ORDER BY
		date_created ASC`

	var dbs []integrationDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	result := make([]integrationbus.Integration, 0, len(dbs))
	for _, db := range dbs {
		creds, err := s.decryptCreds(db.CredentialsEnc, db.CredentialsIV)
		if err != nil {
			s.log.Error(ctx, "decrypt integration credentials", "id", db.ID, "err", err)
			continue
		}
		result = append(result, toBusIntegration(db, creds))
	}

	return result, nil
}

// =============================================================================
// Alert Rule CRUD

// CreateRule inserts a new alert rule into the database.
func (s *Store) CreateRule(ctx context.Context, r integrationbus.AlertRule) error {
	const q = `
	INSERT INTO alert_rules
		(id, org_id, connection_id, project_id, name, level, is_active, created_at, updated_at)
	VALUES
		(:id, :org_id, :connection_id, :project_id, :name, :level, :is_active, :created_at, :updated_at)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBAlertRule(r)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// UpdateRule replaces a rule's mutable fields in the database.
func (s *Store) UpdateRule(ctx context.Context, r integrationbus.AlertRule) error {
	const q = `
	UPDATE alert_rules
	SET
		name          = :name,
		level         = :level,
		connection_id = :connection_id,
		project_id    = :project_id,
		is_active     = :is_active,
		updated_at    = :updated_at
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBAlertRule(r)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// DeleteRule removes an alert rule from the database.
func (s *Store) DeleteRule(ctx context.Context, id uuid.UUID) error {
	data := struct {
		ID uuid.UUID `db:"id"`
	}{ID: id}

	const q = `DELETE FROM alert_rules WHERE id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryRuleByID returns the alert rule identified by id.
func (s *Store) QueryRuleByID(ctx context.Context, id uuid.UUID) (integrationbus.AlertRule, error) {
	data := struct {
		ID string `db:"id"`
	}{ID: id.String()}

	const q = `
	SELECT id, org_id, connection_id, project_id, name, level, is_active, created_at, updated_at
	FROM alert_rules
	WHERE id = :id`

	var db alertRuleDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &db); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return integrationbus.AlertRule{}, fmt.Errorf("db: %w", integrationbus.ErrRuleNotFound)
		}
		return integrationbus.AlertRule{}, fmt.Errorf("db: %w", err)
	}

	return toBusAlertRule(db), nil
}

// QueryRulesByOrg returns all alert rules for an org, ordered by creation date.
func (s *Store) QueryRulesByOrg(ctx context.Context, orgID uuid.UUID) ([]integrationbus.AlertRule, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT id, org_id, connection_id, project_id, name, level, is_active, created_at, updated_at
	FROM alert_rules
	WHERE org_id = :org_id
	ORDER BY created_at ASC`

	var dbs []alertRuleDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, q, data, &dbs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	rules := make([]integrationbus.AlertRule, len(dbs))
	for i, db := range dbs {
		rules[i] = toBusAlertRule(db)
	}

	return rules, nil
}

// DisableRulesByConnection sets is_active=false on all rules referencing a connection.
func (s *Store) DisableRulesByConnection(ctx context.Context, connectionID uuid.UUID) error {
	data := struct {
		ConnectionID uuid.UUID `db:"connection_id"`
	}{ConnectionID: connectionID}

	const q = `UPDATE alert_rules SET is_active = false WHERE connection_id = :connection_id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryProviders returns all enabled integration provider definitions, ordered by sort_order.
func (s *Store) QueryProviders(ctx context.Context) ([]integrationbus.Provider, error) {
	const q = `
	SELECT
		id, name, icon, type, description, fields
	FROM
		integration_providers
	WHERE
		enabled = true
	ORDER BY
		sort_order ASC`

	var dbs []providerDB
	if err := sqldb.QuerySlice(ctx, s.log, s.db, q, &dbs); err != nil {
		return nil, fmt.Errorf("queryslice: %w", err)
	}

	providers := make([]integrationbus.Provider, 0, len(dbs))
	for _, db := range dbs {
		p, err := toBusProvider(db)
		if err != nil {
			s.log.Error(ctx, "parse provider fields", "id", db.ID, "err", err)
			continue
		}
		providers = append(providers, p)
	}

	return providers, nil
}
