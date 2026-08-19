// Package ssodb contains SSO configuration database access. The OIDC client
// secret is sealed with AES-GCM using the same key that protects integration
// credentials, so a database dump alone does not disclose it.
package ssodb

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages SSO configuration data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
	key []byte
}

// NewStore constructs the api for data access. key must be a valid AES key
// (16, 24 or 32 bytes).
func NewStore(log *logger.Logger, db *sqlx.DB, key []byte) (*Store, error) {
	if _, err := aes.NewCipher(key); err != nil {
		return nil, fmt.Errorf("invalid encryption key: %w", err)
	}
	return &Store{log: log, db: db, key: key}, nil
}

type configDB struct {
	OrgID          uuid.UUID      `db:"org_id"`
	Issuer         string         `db:"issuer"`
	ClientID       string         `db:"client_id"`
	SecretEnc      []byte         `db:"client_secret_enc"`
	SecretIV       []byte         `db:"client_secret_iv"`
	DefaultRole    string         `db:"default_role"`
	AllowedDomains dbarray.String `db:"allowed_domains"`
	Enabled        bool           `db:"enabled"`
	DateCreated    time.Time      `db:"date_created"`
	DateUpdated    time.Time      `db:"date_updated"`
}

// Upsert writes an organization's configuration, replacing any existing row.
func (s *Store) Upsert(ctx context.Context, cfg ssobus.Config) error {
	enc, iv, err := s.seal(cfg.ClientSecret)
	if err != nil {
		return err
	}

	domains := make(dbarray.String, len(cfg.AllowedDomains))
	copy(domains, cfg.AllowedDomains)

	row := configDB{
		OrgID:          cfg.OrgID,
		Issuer:         cfg.Issuer,
		ClientID:       cfg.ClientID,
		SecretEnc:      enc,
		SecretIV:       iv,
		DefaultRole:    cfg.DefaultRole.String(),
		AllowedDomains: domains,
		Enabled:        cfg.Enabled,
		DateCreated:    cfg.DateCreated.UTC(),
		DateUpdated:    cfg.DateUpdated.UTC(),
	}

	const q = `
	INSERT INTO org_sso_configs
		(org_id, issuer, client_id, client_secret_enc, client_secret_iv,
		 default_role, allowed_domains, enabled, date_created, date_updated)
	VALUES
		(:org_id, :issuer, :client_id, :client_secret_enc, :client_secret_iv,
		 :default_role, :allowed_domains, :enabled, :date_created, :date_updated)
	ON CONFLICT (org_id) DO UPDATE SET
		issuer = EXCLUDED.issuer,
		client_id = EXCLUDED.client_id,
		client_secret_enc = EXCLUDED.client_secret_enc,
		client_secret_iv = EXCLUDED.client_secret_iv,
		default_role = EXCLUDED.default_role,
		allowed_domains = EXCLUDED.allowed_domains,
		enabled = EXCLUDED.enabled,
		date_updated = EXCLUDED.date_updated`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, row); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// QueryByOrg returns an organization's configuration with the secret decrypted.
func (s *Store) QueryByOrg(ctx context.Context, orgID uuid.UUID) (ssobus.Config, error) {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `
	SELECT org_id, issuer, client_id, client_secret_enc, client_secret_iv,
	       default_role, allowed_domains, enabled, date_created, date_updated
	FROM org_sso_configs
	WHERE org_id = :org_id`

	var row configDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &row); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return ssobus.Config{}, fmt.Errorf("db: %w", ssobus.ErrNotFound)
		}
		return ssobus.Config{}, fmt.Errorf("db: %w", err)
	}

	secret, err := s.open(row.SecretEnc, row.SecretIV)
	if err != nil {
		return ssobus.Config{}, err
	}

	r, err := role.Parse(row.DefaultRole)
	if err != nil {
		return ssobus.Config{}, fmt.Errorf("parse default_role: %w", err)
	}

	allowed := make([]string, len(row.AllowedDomains))
	copy(allowed, row.AllowedDomains)

	return ssobus.Config{
		OrgID:          row.OrgID,
		Issuer:         row.Issuer,
		ClientID:       row.ClientID,
		ClientSecret:   secret,
		DefaultRole:    r,
		AllowedDomains: allowed,
		Enabled:        row.Enabled,
		DateCreated:    row.DateCreated.In(time.Local),
		DateUpdated:    row.DateUpdated.In(time.Local),
	}, nil
}

// Delete removes an organization's configuration.
func (s *Store) Delete(ctx context.Context, orgID uuid.UUID) error {
	data := struct {
		OrgID string `db:"org_id"`
	}{OrgID: orgID.String()}

	const q = `DELETE FROM org_sso_configs WHERE org_id = :org_id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

func (s *Store) seal(secret string) (enc, iv []byte, err error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("new gcm: %w", err)
	}

	iv = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}

	return gcm.Seal(nil, iv, []byte(secret), nil), iv, nil
}

func (s *Store) open(enc, iv []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}

	plain, err := gcm.Open(nil, iv, enc, nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}

	return string(plain), nil
}
