package annotationapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log           *logger.Logger
	AuthClient    authclient.Authenticator
	AnnotationBus *annotationbus.Business
	LogBus        logbus.ExtBusiness
	OrgBus        orgbus.ExtBusiness
	ProjectBus    projectbus.ExtBusiness
}

// Routes adds specific routes for this group.
//
// Membership gates every route; which projects a caller may annotate or read is
// resolved per request, so a viewer with access to one project sees only that
// project's notes.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgMember := mid.AuthorizeOrgMember(cfg.OrgBus)

	api := newApp(cfg)

	base := "/orgs/{org_id}/annotations"
	app.HandlerFunc(http.MethodGet, version, base, api.query, authen, orgMember)
	app.HandlerFunc(http.MethodPost, version, base, api.create, authen, orgMember)
	app.HandlerFunc(http.MethodPatch, version, base+"/{annotation_id}", api.update, authen, orgMember)
	app.HandlerFunc(http.MethodDelete, version, base+"/{annotation_id}", api.remove, authen, orgMember)
}
