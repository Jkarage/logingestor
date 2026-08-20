package logapp

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
)

func req(t *testing.T, qs string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "/v1/orgs/x/logs?"+qs, nil)
}

func Test_selectProjects_DefaultsToEverythingVisible(t *testing.T) {
	visible := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	got, resp := selectProjects(req(t, ""), visible)
	if resp != nil {
		t.Fatalf("unexpected error: %v", resp)
	}
	if !reflect.DeepEqual(got, visible) {
		t.Errorf("omitting projectId must select every visible project")
	}
}

func Test_selectProjects_Narrows(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	visible := []uuid.UUID{a, b, c}

	t.Run("repeatable and comma-separated are equivalent", func(t *testing.T) {
		one, r1 := selectProjects(req(t, "projectId="+a.String()+"&projectId="+b.String()), visible)
		two, r2 := selectProjects(req(t, "projectId="+a.String()+","+b.String()), visible)
		if r1 != nil || r2 != nil {
			t.Fatalf("unexpected errors: %v %v", r1, r2)
		}
		if !reflect.DeepEqual(one, two) || len(one) != 2 {
			t.Errorf("got %v and %v, want the same two projects", one, two)
		}
	})

	t.Run("duplicates collapse", func(t *testing.T) {
		got, resp := selectProjects(req(t, "projectId="+a.String()+","+a.String()), visible)
		if resp != nil {
			t.Fatalf("unexpected error: %v", resp)
		}
		// A repeated id must not fan out twice; the LATERAL would scan it twice
		// and return duplicate rows.
		if len(got) != 1 {
			t.Errorf("got %d projects, want 1", len(got))
		}
	})
}

func Test_selectProjects_Rejections(t *testing.T) {
	a := uuid.New()
	invisible := uuid.New()
	visible := []uuid.UUID{a}

	// Asking for a project you cannot see must not silently return an empty page
	// or somebody else's rows.
	t.Run("invisible project is not found", func(t *testing.T) {
		_, resp := selectProjects(req(t, "projectId="+invisible.String()), visible)
		e, ok := resp.(*errs.Error)
		if !ok || e.Code != errs.NotFound {
			t.Fatalf("got %T %v, want NotFound", resp, resp)
		}
	})

	t.Run("malformed id is a bad request", func(t *testing.T) {
		_, resp := selectProjects(req(t, "projectId=not-a-uuid"), visible)
		e, ok := resp.(*errs.Error)
		if !ok || e.Code != errs.InvalidArgument {
			t.Fatalf("got %T %v, want InvalidArgument", resp, resp)
		}
	})

	// The fan-out cost is linear in the project count, so both the explicit list
	// and the implicit "everything" are capped.
	t.Run("too many requested", func(t *testing.T) {
		var ids string
		big := make([]uuid.UUID, 0, MaxOrgLogProjects+1)
		for i := 0; i <= MaxOrgLogProjects; i++ {
			id := uuid.New()
			big = append(big, id)
			if i > 0 {
				ids += ","
			}
			ids += id.String()
		}
		if _, resp := selectProjects(req(t, "projectId="+ids), big); resp == nil {
			t.Error("expected a cap on the number of projectId values")
		}
	})

	t.Run("too many visible with no explicit list", func(t *testing.T) {
		big := make([]uuid.UUID, MaxOrgLogProjects+1)
		for i := range big {
			big[i] = uuid.New()
		}
		_, resp := selectProjects(req(t, ""), big)
		e, ok := resp.(*errs.Error)
		if !ok || e.Code != errs.InvalidArgument {
			t.Fatalf("got %T %v, want InvalidArgument telling the caller to pass projectId", resp, resp)
		}
	})
}
