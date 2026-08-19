package ssoapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/app/sdk/oidc"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/password"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// sessionTTL is how long a session token minted by SSO stays valid.
const sessionTTL = 12 * time.Hour

type app struct {
	log      *logger.Logger
	ssoBus   *ssobus.Business
	scimBus  *scimbus.Business
	orgBus   orgbus.ExtBusiness
	userBus  userbus.ExtBusiness
	auth     *auth.Auth
	oidc     *oidc.Client
	states   *stateStore
	sessions *sessionStore

	signingKey           string
	callbackURL          string
	completeURL          string
	requireVerifiedEmail bool
}

func newApp(cfg Config) *app {
	return &app{
		log:                  cfg.Log,
		ssoBus:               cfg.SSOBus,
		scimBus:              cfg.SCIMBus,
		orgBus:               cfg.OrgBus,
		userBus:              cfg.UserBus,
		auth:                 cfg.Auth,
		oidc:                 oidc.New(0),
		states:               newStateStore(),
		sessions:             newSessionStore(),
		signingKey:           cfg.SigningKey,
		callbackURL:          cfg.CallbackURL,
		completeURL:          cfg.CompleteURL,
		requireVerifiedEmail: cfg.RequireVerifiedEmail,
	}
}

// =============================================================================
// Configuration, org-admin only

// query returns the org's provider configuration, without the client secret.
// GET /v1/orgs/{org_id}/sso
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	cfg, err := a.ssoBus.QueryByOrg(ctx, orgID)
	if err != nil {
		if errors.Is(err, ssobus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("no SSO provider is configured for this organization"))
		}
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	return toAppSSOConfig(cfg)
}

// upsert configures or reconfigures the org's provider.
// PUT /v1/orgs/{org_id}/sso
func (a *app) upsert(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	var body UpsertSSOConfig
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	nc, err := toBusNewConfig(body)
	if err != nil {
		var appErr *errs.Error
		if errors.As(err, &appErr) {
			return appErr
		}
		return errs.New(errs.InvalidArgument, err)
	}

	// Reject a provider we cannot actually discover, so a typo surfaces here
	// rather than as a broken login for every member of the org.
	if _, err := a.oidc.Discover(ctx, nc.Issuer); err != nil {
		return errs.Errorf(errs.InvalidArgument, "could not reach the issuer's OpenID configuration: %s", err)
	}

	cfg, err := a.ssoBus.Upsert(ctx, orgID, nc)
	if err != nil {
		if errors.Is(err, ssobus.ErrInvalidIssuer) || errors.Is(err, ssobus.ErrInvalidRole) {
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "upsert: orgID[%s]: %s", orgID, err)
	}

	return toAppSSOConfig(cfg)
}

// remove deletes the org's provider configuration.
// DELETE /v1/orgs/{org_id}/sso
func (a *app) remove(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if err := a.ssoBus.Delete(ctx, orgID); err != nil {
		return errs.Errorf(errs.Internal, "delete: orgID[%s]: %s", orgID, err)
	}

	return nil
}

// =============================================================================
// Login flow, public

// start redirects the browser to the org's identity provider.
// GET /v1/auth/sso/{org_slug}/start
func (a *app) start(ctx context.Context, r *http.Request) web.Encoder {
	slug := web.Param(r, "org_slug")

	org, err := a.orgBus.QueryBySlug(ctx, slug)
	if err != nil {
		// Do not distinguish "no such org" from "SSO not set up": either way the
		// caller learns nothing about which orgs exist.
		return errs.New(errs.NotFound, errors.New("single sign-on is not available for this organization"))
	}

	cfg, err := a.ssoBus.QueryByOrg(ctx, org.ID)
	if err != nil || !cfg.Enabled {
		return errs.New(errs.NotFound, errors.New("single sign-on is not available for this organization"))
	}

	provider, err := a.oidc.Discover(ctx, cfg.Issuer)
	if err != nil {
		a.log.Error(ctx, "sso: discovery failed", "orgID", org.ID, "issuer", cfg.Issuer, "msg", err)
		return errs.New(errs.Unavailable, errors.New("the identity provider is unreachable"))
	}

	state, nonce, challenge, err := a.states.begin(org.ID, time.Now())
	if err != nil {
		return errs.Errorf(errs.Internal, "begin sso state: %s", err)
	}

	return redirect(provider.AuthCodeURL(cfg.ClientID, a.callbackURL, state, nonce, challenge))
}

// callback completes the authorization-code flow and hands the SPA a one-time
// code it can exchange for a session token.
// GET /v1/auth/sso/callback
func (a *app) callback(ctx context.Context, r *http.Request) web.Encoder {
	q := r.URL.Query()

	if e := q.Get("error"); e != "" {
		return redirect(a.completeURL + "?error=" + url.QueryEscape(e))
	}

	ls, err := a.states.consume(q.Get("state"), time.Now())
	if err != nil {
		return redirect(a.completeURL + "?error=invalid_state")
	}

	cfg, err := a.ssoBus.QueryByOrg(ctx, ls.orgID)
	if err != nil || !cfg.Enabled {
		return redirect(a.completeURL + "?error=sso_unavailable")
	}

	provider, err := a.oidc.Discover(ctx, cfg.Issuer)
	if err != nil {
		return redirect(a.completeURL + "?error=provider_unreachable")
	}

	rawIDToken, err := a.oidc.Exchange(ctx, provider, cfg.ClientID, cfg.ClientSecret, a.callbackURL, q.Get("code"), ls.codeVerifier)
	if err != nil {
		a.log.Error(ctx, "sso: code exchange failed", "orgID", ls.orgID, "msg", err)
		return redirect(a.completeURL + "?error=exchange_failed")
	}

	identity, err := a.oidc.VerifyIDToken(ctx, provider, rawIDToken, cfg.ClientID, ls.nonce, a.requireVerifiedEmail)
	if err != nil {
		a.log.Error(ctx, "sso: id_token verification failed", "orgID", ls.orgID, "msg", err)
		return redirect(a.completeURL + "?error=verification_failed")
	}

	if !cfg.PermitsEmail(identity.Email) {
		a.log.Info(ctx, "sso: email domain not allowed", "orgID", ls.orgID)
		return redirect(a.completeURL + "?error=domain_not_allowed")
	}

	usr, err := a.provision(ctx, cfg, identity)
	if err != nil {
		a.log.Error(ctx, "sso: provisioning failed", "orgID", ls.orgID, "msg", err)
		return redirect(a.completeURL + "?error=provisioning_failed")
	}

	token, err := a.mintSession(usr, ls.orgID)
	if err != nil {
		a.log.Error(ctx, "sso: mint session failed", "userID", usr.ID, "msg", err)
		return redirect(a.completeURL + "?error=session_failed")
	}

	code, err := a.sessions.put(token, time.Now())
	if err != nil {
		return redirect(a.completeURL + "?error=session_failed")
	}

	// Only the one-time code travels in the URL; the JWT is collected over POST.
	return redirect(a.completeURL + "?code=" + url.QueryEscape(code))
}

// exchange trades the one-time code for the session token.
// POST /v1/auth/sso/exchange
func (a *app) exchange(ctx context.Context, r *http.Request) web.Encoder {
	var body ExchangeRequest
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	token, err := a.sessions.take(body.Code, time.Now())
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	return SessionResponse{Token: token, ExpiresInSeconds: int(sessionTTL.Seconds())}
}

// =============================================================================

// provision resolves the IdP identity to a local user, creating one on first
// login, and ensures the user is a member of the org.
func (a *app) provision(ctx context.Context, cfg ssobus.Config, identity oidc.Identity) (userbus.User, error) {
	addr, err := mail.ParseAddress(identity.Email)
	if err != nil {
		return userbus.User{}, fmt.Errorf("parse email: %w", err)
	}

	usr, err := a.userBus.QueryByEmail(ctx, *addr)
	switch {
	case err == nil:
		// Existing account: SSO is an additional way in, not a new identity.
	case errors.Is(err, userbus.ErrNotFound):
		usr, err = a.createSSOUser(ctx, identity, *addr)
		if err != nil {
			return userbus.User{}, err
		}
	default:
		return userbus.User{}, fmt.Errorf("querybyemail: %w", err)
	}

	// Just-in-time membership at the configured default role. An existing
	// membership is left alone, so a promotion inside the app is never undone by
	// the next login.
	if _, err := a.orgBus.AddMember(ctx, usr.ID, orgbus.NewOrgMember{
		OrgID:  cfg.OrgID,
		UserID: usr.ID,
		Role:   cfg.DefaultRole,
	}); err != nil && !errors.Is(err, orgbus.ErrMemberExists) {
		return userbus.User{}, fmt.Errorf("addmember: %w", err)
	}

	return usr, nil
}

// createSSOUser creates a local account for an IdP-asserted identity. The
// account is enabled immediately because the provider has already verified the
// address, and it gets an unguessable random password the user never learns —
// SSO is the only way in unless they run a password reset.
func (a *app) createSSOUser(ctx context.Context, identity oidc.Identity, addr mail.Address) (userbus.User, error) {
	display := identity.Name
	if display == "" {
		display = identity.Email
	}

	nme, err := name.Parse(display)
	if err != nil {
		// Fall back to the local part if the IdP's display name is unusable.
		nme, err = name.Parse(addr.Address[:min(len(addr.Address), 19)])
		if err != nil {
			return userbus.User{}, fmt.Errorf("parse name: %w", err)
		}
	}

	pw, err := randomPassword()
	if err != nil {
		return userbus.User{}, err
	}

	usr, err := a.userBus.Create(ctx, uuid.UUID{}, userbus.NewUser{
		Name:     nme,
		Email:    addr,
		Roles:    []role.Role{role.User},
		Password: pw,
	})
	if err != nil {
		return userbus.User{}, fmt.Errorf("create user: %w", err)
	}

	// userbus.Create leaves accounts disabled pending email verification; the IdP
	// has already done that, so enable it or the new user cannot authenticate.
	enabled := true
	usr, err = a.userBus.Update(ctx, usr.ID, usr, userbus.UpdateUser{Enabled: &enabled})
	if err != nil {
		return userbus.User{}, fmt.Errorf("enable user: %w", err)
	}

	return usr, nil
}

// mintSession issues one of our own JWTs for the authenticated user.
func (a *app) mintSession(usr userbus.User, orgID uuid.UUID) (string, error) {
	now := time.Now().UTC()

	return a.auth.GenerateToken(a.signingKey, auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   usr.ID.String(),
			Issuer:    a.auth.Issuer(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(sessionTTL)),
		},
		Roles: role.ParseToString(usr.Roles),
		OrgID: orgID.String(),
	})
}

// passwordAlphabet matches what business/types/password accepts.
const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789#@!-"

// randomPassword returns a 19-character random password — the longest the
// password type permits, giving ~115 bits of entropy.
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

// redirectEncoder issues a 302 without a response body.
type redirectEncoder string

func (r redirectEncoder) Encode() ([]byte, string, error) { return nil, "", nil }

// HTTPStatus implements the web package's status interface.
func (r redirectEncoder) HTTPStatus() int { return http.StatusFound }

// Location is read by the router to set the redirect header.
func (r redirectEncoder) Location() string { return string(r) }

func redirect(to string) web.Encoder { return redirectEncoder(to) }

// =============================================================================
// SCIM provisioning token, org-admin only

// SCIMTokenInfo describes an org's SCIM token without disclosing it.
type SCIMTokenInfo struct {
	Configured  bool    `json:"configured"`
	TokenPrefix string  `json:"tokenPrefix,omitempty"`
	DateCreated string  `json:"dateCreated,omitempty"`
	LastUsedAt  *string `json:"lastUsedAt"`
}

// Encode implements the encoder interface.
func (app SCIMTokenInfo) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// SCIMTokenIssued carries the raw token, returned exactly once.
type SCIMTokenIssued struct {
	Token       string `json:"token"`
	TokenPrefix string `json:"tokenPrefix"`
}

// Encode implements the encoder interface.
func (app SCIMTokenIssued) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// querySCIMToken reports whether SCIM is configured, and when it was last used.
// GET /v1/orgs/{org_id}/scim-token
func (a *app) querySCIMToken(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	t, err := a.scimBus.QueryByOrg(ctx, orgID)
	if err != nil {
		if errors.Is(err, scimbus.ErrNotFound) {
			return SCIMTokenInfo{Configured: false}
		}
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	info := SCIMTokenInfo{
		Configured:  true,
		TokenPrefix: t.TokenPrefix,
		DateCreated: t.DateCreated.Format(time.RFC3339),
	}
	if t.LastUsedAt != nil {
		v := t.LastUsedAt.Format(time.RFC3339)
		info.LastUsedAt = &v
	}

	return info
}

// issueSCIMToken mints a token, replacing any existing one. Issuing is also how
// rotation works, so the previous token stops being accepted immediately.
// POST /v1/orgs/{org_id}/scim-token
func (a *app) issueSCIMToken(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	t, raw, err := a.scimBus.Issue(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "issue scim token: orgID[%s]: %s", orgID, err)
	}

	return SCIMTokenIssued{Token: raw, TokenPrefix: t.TokenPrefix}
}

// revokeSCIMToken deletes the org's token, stopping all provisioning.
// DELETE /v1/orgs/{org_id}/scim-token
func (a *app) revokeSCIMToken(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if err := a.scimBus.Revoke(ctx, orgID); err != nil {
		return errs.Errorf(errs.Internal, "revoke scim token: orgID[%s]: %s", orgID, err)
	}

	return nil
}
