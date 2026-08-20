package orgapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jkarage/logingestor/foundation/web"
)

func Test_legacyRoleAlias(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{
			name:   "the legacy role path is rewritten",
			method: http.MethodPut,
			path:   "/v1/orgs/role/4d3c2b1a-0000-0000-0000-000000000000",
			want:   "/v1/orgs/4d3c2b1a-0000-0000-0000-000000000000/role",
		},
		{
			// Only PUT ever lived at the old path; rewriting other methods would
			// invent routes that never existed.
			name:   "another method is left alone",
			method: http.MethodGet,
			path:   "/v1/orgs/role/4d3c2b1a-0000-0000-0000-000000000000",
		},
		{
			name:   "the canonical path is left alone",
			method: http.MethodPut,
			path:   "/v1/orgs/4d3c2b1a-0000-0000-0000-000000000000/role",
		},
		{
			// This is the org update, which shares the prefix up to the id.
			name:   "an org named role is not rewritten",
			method: http.MethodPut,
			path:   "/v1/orgs/role",
		},
		{
			name:   "a deeper path is not rewritten into a shape nothing serves",
			method: http.MethodPut,
			path:   "/v1/orgs/role/abc/members",
		},
		{
			name:   "a missing id is not rewritten",
			method: http.MethodPut,
			path:   "/v1/orgs/role/",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := legacyRoleAlias(c.method, c.path)

			if c.want == "" {
				if ok {
					t.Fatalf("rewrote %s %s to %q, want it left alone", c.method, c.path, got)
				}
				return
			}
			if !ok {
				t.Fatalf("did not rewrite %s %s", c.method, c.path)
			}
			if got != c.want {
				t.Errorf("rewrote to %q, want %q", got, c.want)
			}
		})
	}
}

// The rewrite has to actually route: a caller on the old path must reach the new
// handler with the org id extracted, or the migration breaks the one call site
// it was meant to protect.
func Test_legacyRoleAlias_RoutesToTheCanonicalHandler(t *testing.T) {
	app := web.NewApp(func(context.Context, string, ...any) {}, nil)
	app.AddAlias(legacyRoleAlias)

	var gotOrgID string
	app.HandlerFunc(http.MethodPut, "v1", "/orgs/{org_id}/role", func(_ context.Context, r *http.Request) web.Encoder {
		gotOrgID = web.Param(r, "org_id")
		return nil
	})

	const orgID = "4d3c2b1a-0000-0000-0000-000000000000"

	r := httptest.NewRequest(http.MethodPut, "/v1/orgs/role/"+orgID, strings.NewReader(`{"memberId":"x","role":"VIEWER"}`))
	w := httptest.NewRecorder()

	app.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: the legacy path did not reach the handler", w.Code)
	}
	if gotOrgID != orgID {
		t.Errorf("org_id = %q, want %q", gotOrgID, orgID)
	}

	// The response says the path is retired, so a caller can find its remaining
	// call sites without reading our logs.
	if w.Header().Get("Deprecation") != "true" {
		t.Errorf("Deprecation header = %q, want true", w.Header().Get("Deprecation"))
	}
	if link := w.Header().Get("Link"); !strings.Contains(link, "/v1/orgs/"+orgID+"/role") {
		t.Errorf("Link header = %q, want it to name the successor path", link)
	}
}

// The canonical path must serve without any deprecation noise.
func Test_RoleRoute_CanonicalPathIsNotDeprecated(t *testing.T) {
	app := web.NewApp(func(context.Context, string, ...any) {}, nil)
	app.AddAlias(legacyRoleAlias)

	called := false
	app.HandlerFunc(http.MethodPut, "v1", "/orgs/{org_id}/role", func(_ context.Context, r *http.Request) web.Encoder {
		called = true
		return nil
	})

	r := httptest.NewRequest(http.MethodPut, "/v1/orgs/4d3c2b1a-0000-0000-0000-000000000000/role", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	app.ServeHTTP(w, r)

	if !called {
		t.Fatalf("the canonical path did not reach the handler (status %d)", w.Code)
	}
	if w.Header().Get("Deprecation") != "" {
		t.Errorf("the canonical path was marked deprecated")
	}
}
