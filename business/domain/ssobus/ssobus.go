// Package ssobus provides per-organization single sign-on configuration. One
// OIDC identity provider may be configured per org; the client secret is
// encrypted at rest by the store.
package ssobus

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("sso config not found")
	ErrInvalidIssuer = errors.New("issuer must be an https URL with no query or fragment")
	ErrInvalidRole   = errors.New("defaultRole must be ORG ADMIN, PROJECT MANAGER, or VIEWER")
)

// Config is an organization's OIDC provider configuration.
type Config struct {
	OrgID    uuid.UUID
	Issuer   string
	ClientID string

	// ClientSecret is only populated in memory; the store seals it at rest and
	// the API never returns it.
	ClientSecret string

	// DefaultRole is granted to a just-in-time membership created on first SSO
	// login.
	DefaultRole role.Role

	// AllowedDomains, when non-empty, restricts which email domains may sign in
	// through this provider. Without it, any account the IdP asserts is accepted.
	AllowedDomains []string

	Enabled     bool
	DateCreated time.Time
	DateUpdated time.Time
}

// PermitsEmail reports whether email is allowed by the domain allow-list. An
// empty list permits everything.
func (c Config) PermitsEmail(email string) bool {
	if len(c.AllowedDomains) == 0 {
		return true
	}

	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])

	for _, d := range c.AllowedDomains {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(d), "@"), domain) {
			return true
		}
	}

	return false
}

// NewConfig is the data needed to configure or reconfigure a provider.
type NewConfig struct {
	Issuer         string
	ClientID       string
	ClientSecret   string
	DefaultRole    role.Role
	AllowedDomains []string
	Enabled        bool
}

// Validate checks a submitted configuration.
//
// The issuer must be https: discovery, the token exchange and JWKS retrieval all
// trust it, so allowing http would let a network attacker impersonate the IdP.
func (nc NewConfig) Validate() error {
	u, err := url.Parse(strings.TrimSpace(nc.Issuer))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return ErrInvalidIssuer
	}

	if nc.ClientID == "" || nc.ClientSecret == "" {
		return errors.New("clientId and clientSecret are required")
	}

	// SUPER ADMIN is a platform role and must never be grantable by an org's IdP.
	if nc.DefaultRole == role.Admin || nc.DefaultRole == (role.Role{}) {
		return ErrInvalidRole
	}

	return nil
}

// Storer declares the behavior this package needs to persist data.
type Storer interface {
	Upsert(ctx context.Context, cfg Config) error
	QueryByOrg(ctx context.Context, orgID uuid.UUID) (Config, error)
	Delete(ctx context.Context, orgID uuid.UUID) error
}

// Business manages the set of APIs for SSO configuration.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs an SSO business API for use.
func NewBusiness(log *logger.Logger, storer Storer) *Business {
	return &Business{log: log, storer: storer}
}

// Upsert stores an organization's provider configuration, replacing any existing
// one.
func (b *Business) Upsert(ctx context.Context, orgID uuid.UUID, nc NewConfig) (Config, error) {
	if err := nc.Validate(); err != nil {
		return Config{}, err
	}

	now := time.Now()

	cfg := Config{
		OrgID:          orgID,
		Issuer:         strings.TrimSuffix(strings.TrimSpace(nc.Issuer), "/"),
		ClientID:       nc.ClientID,
		ClientSecret:   nc.ClientSecret,
		DefaultRole:    nc.DefaultRole,
		AllowedDomains: nc.AllowedDomains,
		Enabled:        nc.Enabled,
		DateCreated:    now,
		DateUpdated:    now,
	}

	if err := b.storer.Upsert(ctx, cfg); err != nil {
		return Config{}, fmt.Errorf("upsert: %w", err)
	}

	return cfg, nil
}

// QueryByOrg returns an organization's provider configuration.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) (Config, error) {
	cfg, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return Config{}, fmt.Errorf("querybyorg: %w", err)
	}
	return cfg, nil
}

// Delete removes an organization's provider configuration.
func (b *Business) Delete(ctx context.Context, orgID uuid.UUID) error {
	if err := b.storer.Delete(ctx, orgID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
