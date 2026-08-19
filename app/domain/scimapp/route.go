package scimapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/password"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log     *logger.Logger
	SCIMBus *scimbus.Business
	OrgBus  orgbus.ExtBusiness
	UserBus userbus.ExtBusiness

	// DefaultRoles supplies the role a newly provisioned member receives.
	DefaultRoles defaultRoleSource

	// BaseURL is this SCIM service's root, used to build resource Location values.
	BaseURL string
}

// SSODefaultRole adapts the SSO config store to defaultRoleSource so SCIM and
// SSO agree on the role a new member gets. It falls back to VIEWER when no SSO
// provider is configured, keeping provisioning least-privileged.
type SSODefaultRole struct {
	SSOBus *ssobus.Business
}

// DefaultRole implements defaultRoleSource.
func (s SSODefaultRole) DefaultRole(ctx context.Context, orgID uuid.UUID) role.Role {
	if s.SSOBus == nil {
		return role.User
	}

	cfg, err := s.SSOBus.QueryByOrg(ctx, orgID)
	if err != nil || cfg.DefaultRole == (role.Role{}) {
		return role.User
	}

	return cfg.DefaultRole
}

// Routes adds specific routes for this group.
//
// SCIM endpoints authenticate with a per-org provisioning token rather than a
// user JWT, so they carry none of the normal auth middleware. The token resolves
// to exactly one organization inside each handler.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	api := newApp(cfg)

	base := "/scim/v2/Users"
	app.HandlerFunc(http.MethodGet, version, base, api.queryUsers)
	app.HandlerFunc(http.MethodPost, version, base, api.createUser)
	app.HandlerFunc(http.MethodGet, version, base+"/{scim_id}", api.getUser)
	app.HandlerFunc(http.MethodPut, version, base+"/{scim_id}", api.putUser)
	app.HandlerFunc(http.MethodPatch, version, base+"/{scim_id}", api.patchUser)
	app.HandlerFunc(http.MethodDelete, version, base+"/{scim_id}", api.deleteUser)
}

// created201 wraps a user so a successful create returns 201, as SCIM requires.
type created201 struct{ User }

// HTTPStatus implements the web package status interface.
func (created201) HTTPStatus() int { return http.StatusCreated }

// Encode implements the encoder interface.
func (c created201) Encode() ([]byte, string, error) {
	data, err := json.Marshal(c.User)
	return data, contentTypeSCIM, err
}

// noContent is an empty 204 response for a successful delete.
type noContent struct{}

// HTTPStatus implements the web package status interface.
func (noContent) HTTPStatus() int { return http.StatusNoContent }

// Encode implements the encoder interface.
func (noContent) Encode() ([]byte, string, error) { return nil, contentTypeSCIM, nil }

// passwordAlphabet matches what business/types/password accepts.
const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789#@!-"

// randomPassword returns a 19-character random password — the longest the
// password type permits. A SCIM-provisioned user never learns it, so SSO is the
// only way in unless they run a password reset.
func randomPassword() (password.Password, error) {
	buf := make([]byte, 19)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
		if err != nil {
			return password.Password{}, fmt.Errorf("read random: %w", err)
		}
		buf[i] = passwordAlphabet[n.Int64()]
	}

	pw, err := password.Parse(string(buf))
	if err != nil {
		return password.Password{}, fmt.Errorf("parse generated password: %w", err)
	}

	return pw, nil
}
