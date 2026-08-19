package scimapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	log     *logger.Logger
	scimBus *scimbus.Business
	orgBus  orgbus.ExtBusiness
	userBus userbus.ExtBusiness
	ssoBus  defaultRoleSource
	baseURL string
}

// defaultRoleSource supplies the role a provisioned member receives. It is the
// SSO config's defaultRole, so SCIM and SSO agree on what a new member gets.
type defaultRoleSource interface {
	DefaultRole(ctx context.Context, orgID uuid.UUID) role.Role
}

func newApp(cfg Config) *app {
	return &app{
		log:     cfg.Log,
		scimBus: cfg.SCIMBus,
		orgBus:  cfg.OrgBus,
		userBus: cfg.UserBus,
		ssoBus:  cfg.DefaultRoles,
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
	}
}

// orgFromToken authenticates the SCIM bearer token and returns the org it
// provisions for. Every handler starts here, so a token can only ever act on its
// own organization.
func (a *app) orgFromToken(ctx context.Context, r *http.Request) (uuid.UUID, web.Encoder) {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return uuid.UUID{}, scimErr(http.StatusUnauthorized, "", "expected authorization header format: Bearer <token>")
	}

	orgID, err := a.scimBus.Authenticate(ctx, parts[1])
	if err != nil {
		return uuid.UUID{}, scimErr(http.StatusUnauthorized, "", "invalid SCIM token")
	}

	return orgID, nil
}

// memberOf returns the caller's membership row for orgID, or nil when absent.
func (a *app) memberOf(ctx context.Context, orgID, userID uuid.UUID) (*orgbus.OrgMember, error) {
	members, err := a.orgBus.QueryMembers(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querymembers: %w", err)
	}

	for i := range members {
		if members[i].UserID == userID {
			return &members[i], nil
		}
	}

	return nil, nil
}

// queryUsers lists provisioned users, optionally filtered by userName.
// GET /v1/scim/v2/Users?filter=userName eq "a@b.c"
func (a *app) queryUsers(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	// SCIM defines a full filter grammar; IdPs in practice only send
	// `userName eq "..."` when reconciling, so that is what is supported. Any
	// other filter is refused rather than silently ignored, which would make a
	// provisioning agent believe a user does not exist and create a duplicate.
	if raw := r.URL.Query().Get("filter"); raw != "" {
		email, ok := parseUserNameEq(raw)
		if !ok {
			return scimErr(http.StatusBadRequest, "invalidFilter", `only filters of the form: userName eq "value" are supported`)
		}

		usr, member, err := a.lookupByEmail(ctx, orgID, email)
		if err != nil {
			return scimErr(http.StatusInternalServerError, "", err.Error())
		}
		if usr == nil || member == nil {
			return ListResponse{Schemas: []string{schemaListResp}, StartIndex: 1, Resources: []User{}}
		}

		return ListResponse{
			Schemas:      []string{schemaListResp},
			TotalResults: 1,
			StartIndex:   1,
			ItemsPerPage: 1,
			Resources:    []User{toSCIMUser(*usr, member, a.baseURL)},
		}
	}

	members, err := a.orgBus.QueryMembersWithUsers(ctx, orgID)
	if err != nil {
		return scimErr(http.StatusInternalServerError, "", "list members failed")
	}

	resources := make([]User, 0, len(members))
	for _, m := range members {
		usr, err := a.userBus.QueryByID(ctx, m.UserID)
		if err != nil {
			continue
		}
		member := orgbus.OrgMember{MemberID: m.MemberID, OrgID: orgID, UserID: m.UserID, Role: m.Role}
		resources = append(resources, toSCIMUser(usr, &member, a.baseURL))
	}

	return ListResponse{
		Schemas:      []string{schemaListResp},
		TotalResults: len(resources),
		StartIndex:   1,
		ItemsPerPage: len(resources),
		Resources:    resources,
	}
}

// getUser returns one provisioned user.
// GET /v1/scim/v2/Users/{scim_id}
func (a *app) getUser(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	userID, err := uuid.Parse(web.Param(r, "scim_id"))
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	usr, err := a.userBus.QueryByID(ctx, userID)
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	member, err := a.memberOf(ctx, orgID, userID)
	if err != nil {
		return scimErr(http.StatusInternalServerError, "", "membership lookup failed")
	}
	// A user outside this organization must be indistinguishable from one that
	// does not exist.
	if member == nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	return toSCIMUser(usr, member, a.baseURL)
}

// createUser provisions a user into the organization.
// POST /v1/scim/v2/Users
func (a *app) createUser(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	var body User
	if err := web.Decode(r, &body); err != nil {
		return scimErr(http.StatusBadRequest, "invalidSyntax", "request body is not valid SCIM JSON")
	}

	email := body.PrimaryEmail()
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return scimErr(http.StatusBadRequest, "invalidValue", "userName must be a valid email address")
	}

	existing, member, err := a.lookupByEmail(ctx, orgID, email)
	if err != nil {
		return scimErr(http.StatusInternalServerError, "", err.Error())
	}

	// SCIM requires 409 when the resource already exists, so the agent updates
	// rather than retrying the create forever.
	if existing != nil && member != nil {
		return scimErr(http.StatusConflict, "uniqueness", "user is already provisioned in this organization")
	}

	usr := existing
	if usr == nil {
		created, err := a.createLocalUser(ctx, body, *addr)
		if err != nil {
			a.log.Error(ctx, "scim: create user", "orgID", orgID, "err", err)
			return scimErr(http.StatusInternalServerError, "", "could not create user")
		}
		usr = &created
	}

	newMember, err := a.orgBus.AddMember(ctx, usr.ID, orgbus.NewOrgMember{
		OrgID:  orgID,
		UserID: usr.ID,
		Role:   a.ssoBus.DefaultRole(ctx, orgID),
	})
	if err != nil && !errors.Is(err, orgbus.ErrMemberExists) {
		a.log.Error(ctx, "scim: add member", "orgID", orgID, "err", err)
		return scimErr(http.StatusInternalServerError, "", "could not provision membership")
	}

	return created201{toSCIMUser(*usr, &newMember, a.baseURL)}
}

// patchUser applies a SCIM PATCH. The operation that matters in practice is
// setting active to false, which an IdP sends on offboarding.
// PATCH /v1/scim/v2/Users/{scim_id}
func (a *app) patchUser(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	userID, err := uuid.Parse(web.Param(r, "scim_id"))
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	var body PatchRequest
	if err := web.Decode(r, &body); err != nil {
		return scimErr(http.StatusBadRequest, "invalidSyntax", "request body is not a valid SCIM PatchOp")
	}

	active, found := activeFromPatch(body)
	if !found {
		// Nothing actionable: report current state rather than erroring, so an
		// agent syncing attributes we do not store still succeeds.
		return a.getUser(ctx, r)
	}

	return a.setActive(ctx, orgID, userID, active)
}

// putUser replaces a user. Only the active flag is actionable.
// PUT /v1/scim/v2/Users/{scim_id}
func (a *app) putUser(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	userID, err := uuid.Parse(web.Param(r, "scim_id"))
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	var body User
	if err := web.Decode(r, &body); err != nil {
		return scimErr(http.StatusBadRequest, "invalidSyntax", "request body is not valid SCIM JSON")
	}

	return a.setActive(ctx, orgID, userID, body.Active)
}

// deleteUser deprovisions a user from the organization.
// DELETE /v1/scim/v2/Users/{scim_id}
func (a *app) deleteUser(ctx context.Context, r *http.Request) web.Encoder {
	orgID, errResp := a.orgFromToken(ctx, r)
	if errResp != nil {
		return errResp
	}

	userID, err := uuid.Parse(web.Param(r, "scim_id"))
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	if resp := a.deprovision(ctx, orgID, userID); resp != nil {
		return resp
	}

	return noContent{}
}

// setActive provisions or deprovisions membership for a user.
func (a *app) setActive(ctx context.Context, orgID, userID uuid.UUID, active bool) web.Encoder {
	usr, err := a.userBus.QueryByID(ctx, userID)
	if err != nil {
		return scimErr(http.StatusNotFound, "", "user not found")
	}

	if !active {
		if resp := a.deprovision(ctx, orgID, userID); resp != nil {
			return resp
		}
		return toSCIMUser(usr, nil, a.baseURL)
	}

	member, err := a.orgBus.AddMember(ctx, userID, orgbus.NewOrgMember{
		OrgID:  orgID,
		UserID: userID,
		Role:   a.ssoBus.DefaultRole(ctx, orgID),
	})
	if err != nil && !errors.Is(err, orgbus.ErrMemberExists) {
		return scimErr(http.StatusInternalServerError, "", "could not provision membership")
	}

	if errors.Is(err, orgbus.ErrMemberExists) {
		existing, err := a.memberOf(ctx, orgID, userID)
		if err != nil || existing == nil {
			return scimErr(http.StatusInternalServerError, "", "membership lookup failed")
		}
		member = *existing
	}

	return toSCIMUser(usr, &member, a.baseURL)
}

// deprovision removes the user's membership in this org.
//
// Only membership is revoked, never the account: the same person may belong to
// other organizations, and one IdP must not be able to delete a user out from
// under another tenant.
func (a *app) deprovision(ctx context.Context, orgID, userID uuid.UUID) web.Encoder {
	member, err := a.memberOf(ctx, orgID, userID)
	if err != nil {
		return scimErr(http.StatusInternalServerError, "", "membership lookup failed")
	}
	if member == nil {
		// Already absent. SCIM deletes are idempotent.
		return nil
	}

	if err := a.orgBus.RemoveMember(ctx, userID, member.MemberID); err != nil {
		return scimErr(http.StatusInternalServerError, "", "could not deprovision membership")
	}

	a.log.Info(ctx, "scim: deprovisioned member", "orgID", orgID, "userID", userID)

	return nil
}

// lookupByEmail resolves an email to a local user and their membership here.
func (a *app) lookupByEmail(ctx context.Context, orgID uuid.UUID, email string) (*userbus.User, *orgbus.OrgMember, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return nil, nil, nil
	}

	usr, err := a.userBus.QueryByEmail(ctx, *addr)
	if err != nil {
		if errors.Is(err, userbus.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("querybyemail: %w", err)
	}

	member, err := a.memberOf(ctx, orgID, usr.ID)
	if err != nil {
		return nil, nil, err
	}

	return &usr, member, nil
}

// createLocalUser creates an account for a SCIM-provisioned identity. It is
// enabled immediately — the IdP is the authority on who exists — and given an
// unguessable password, so SSO is the only way in.
func (a *app) createLocalUser(ctx context.Context, body User, addr mail.Address) (userbus.User, error) {
	display := body.DisplayName()
	if display == "" {
		display = addr.Address
	}

	nme, err := name.Parse(display)
	if err != nil {
		local := addr.Address
		if i := strings.Index(local, "@"); i > 0 {
			local = local[:i]
		}
		if len(local) > 19 {
			local = local[:19]
		}
		nme, err = name.Parse(local)
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

	enabled := true
	usr, err = a.userBus.Update(ctx, usr.ID, usr, userbus.UpdateUser{Enabled: &enabled})
	if err != nil {
		return userbus.User{}, fmt.Errorf("enable user: %w", err)
	}

	return usr, nil
}

// parseUserNameEq extracts the value from `userName eq "value"`.
func parseUserNameEq(filter string) (string, bool) {
	f := strings.TrimSpace(filter)

	lower := strings.ToLower(f)
	if !strings.HasPrefix(lower, "username eq ") {
		return "", false
	}

	v := strings.TrimSpace(f[len("userName eq "):])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if v == "" {
		return "", false
	}

	return strings.ToLower(v), true
}

// activeFromPatch finds an active value in a PATCH document, tolerating the two
// shapes IdPs send: a path of "active", or a replace of the whole resource with
// active inside the value object.
func activeFromPatch(p PatchRequest) (active bool, found bool) {
	for _, op := range p.Operations {
		if !strings.EqualFold(op.Op, "replace") && !strings.EqualFold(op.Op, "add") {
			continue
		}

		if strings.EqualFold(strings.TrimSpace(op.Path), "active") {
			var v any
			if err := json.Unmarshal(op.Value, &v); err != nil {
				continue
			}
			switch t := v.(type) {
			case bool:
				return t, true
			case string:
				return strings.EqualFold(t, "true"), true
			}
			continue
		}

		if op.Path == "" {
			var obj struct {
				Active *bool `json:"active"`
			}
			if err := json.Unmarshal(op.Value, &obj); err == nil && obj.Active != nil {
				return *obj.Active, true
			}
		}
	}

	return false, false
}
