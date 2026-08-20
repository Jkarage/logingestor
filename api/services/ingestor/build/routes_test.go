package build_test

import (
	"io"
	"testing"

	"github.com/jkarage/logingestor/api/services/ingestor/build"
	"github.com/jkarage/logingestor/app/sdk/mux"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Test_Routes_RegisterWithoutConflict registers the entire route table.
//
// Go's ServeMux panics when two patterns are ambiguous — neither more specific
// than the other — and that only happens at registration, so a plain build and
// unit-test pass will not catch it. This exact failure shipped once:
// PUT /v1/orgs/{org_id}/sso was ambiguous with the then-current
// PUT /v1/orgs/role/{org_id}, and the service panicked on boot rather than
// failing anywhere in CI. The role route has since moved to
// PUT /v1/orgs/{org_id}/role, which is what makes both registerable — and what
// this test guards, since re-adding a path with a trailing id would break every
// route under /v1/orgs/{org_id}/ at once.
//
// The buses are left nil on purpose. Route registration must not touch them; if
// a future handler constructor dereferences a bus, this test fails loudly and
// that is worth knowing too.
func Test_Routes_RegisterWithoutConflict(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering routes panicked, so the service cannot start: %v", r)
		}
	}()

	log := logger.New(io.Discard, logger.LevelError, "TEST", nil)
	app := web.NewApp(log.Info, nil)

	build.Routes().Add(app, mux.Config{Log: log})
}
