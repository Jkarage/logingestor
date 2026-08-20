package orgapp

import (
	"net/http"
	"strings"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	Auth       *auth.Auth
	AuthClient authclient.Authenticator
	UserBus    userbus.ExtBusiness
	OrgBus     orgbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleSuperAdmin := mid.Authorize(cfg.AuthClient, auth.RuleAdminOnly)
	ruleOrgMember := mid.AuthorizeOrgMember(cfg.OrgBus)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)
	orgOwner := mid.AuthorizeOrgOwner(cfg.OrgBus)

	api := newApp(cfg.OrgBus, cfg.Auth)

	app.HandlerFunc(http.MethodGet, version, "/orgs/mine", api.queryMine, authen)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/members", api.queryOrgMembers, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/members/{member_id}", api.removeMember, authen, orgAdmin)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}", api.queryByID, authen, ruleOrgMember)
	app.HandlerFunc(http.MethodGet, version, "/orgs", api.query, authen, ruleSuperAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs", api.create, authen)
	app.HandlerFunc(http.MethodPut, version, "/orgs/{org_id}/role", api.updateRole, authen, orgAdmin)
	app.HandlerFunc(http.MethodPut, version, "/orgs/{org_id}", api.update, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}", api.delete, authen, orgOwner)

	// The role update used to live at PUT /v1/orgs/role/{org_id}, which put the
	// id in the trailing segment and made every PUT /v1/orgs/{org_id}/* route
	// unregisterable — ServeMux calls the two ambiguous and panics, which is how
	// the SSO upsert ended up a POST. The old path keeps working through a
	// rewrite until callers move; it cannot be a second registered route for the
	// same reason.
	app.AddAlias(legacyRoleAlias)
}

// legacyRoleAlias rewrites PUT /v1/orgs/role/{orgID} onto the canonical
// PUT /v1/orgs/{orgID}/role.
//
// The id must be a single segment: without that check, any deeper path under
// /v1/orgs/role/ would be rewritten into a shape nothing serves, turning a 404
// into a confusing one.
func legacyRoleAlias(method string, urlPath string) (string, bool) {
	if method != http.MethodPut {
		return "", false
	}

	orgID, ok := strings.CutPrefix(urlPath, "/v1/orgs/role/")
	if !ok || orgID == "" || strings.Contains(orgID, "/") {
		return "", false
	}

	return "/v1/orgs/" + orgID + "/role", true
}
