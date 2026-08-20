package viewbus

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	owner   = uuid.New()
	other   = uuid.New()
	projA   = uuid.New()
	projB   = uuid.New()
	seesA   = Viewer{UserID: other, VisibleProjects: map[uuid.UUID]struct{}{projA: {}}}
	adminA  = Viewer{UserID: other, OrgAdmin: true, VisibleProjects: map[uuid.UUID]struct{}{projA: {}}}
	ownerVw = Viewer{UserID: owner, VisibleProjects: map[uuid.UUID]struct{}{projA: {}}}
)

func Test_CanSeeView_Private(t *testing.T) {
	v := SavedView{Visibility: VisibilityPrivate, CreatedBy: owner}

	if !CanSeeView(v, Viewer{UserID: owner}) {
		t.Error("the creator must see their own private view")
	}
	if CanSeeView(v, Viewer{UserID: other}) {
		t.Error("another member must not see a private view")
	}
	// "private" would mean nothing if an admin could read it.
	if CanSeeView(v, Viewer{UserID: other, OrgAdmin: true}) {
		t.Error("an org admin must not see somebody's private view")
	}
}

func Test_CanSeeView_ProjectPinned(t *testing.T) {
	pinnedA := SavedView{Visibility: VisibilityOrg, CreatedBy: owner, ProjectID: &projA}
	pinnedB := SavedView{Visibility: VisibilityOrg, CreatedBy: owner, ProjectID: &projB}
	orgWide := SavedView{Visibility: VisibilityOrg, CreatedBy: owner}

	if !CanSeeView(pinnedA, seesA) {
		t.Error("a view pinned to a visible project should be readable")
	}
	// Otherwise the name and query leak which projects exist and what is in them.
	if CanSeeView(pinnedB, seesA) {
		t.Error("a view pinned to an invisible project must be hidden")
	}
	if CanSeeView(pinnedB, adminA) {
		t.Error("project visibility gates admins too")
	}
	if !CanSeeView(orgWide, seesA) {
		t.Error("an org-wide view should be readable by any member")
	}

	// A private view pinned to a visible project is still private.
	priv := SavedView{Visibility: VisibilityPrivate, CreatedBy: owner, ProjectID: &projA}
	if CanSeeView(priv, seesA) {
		t.Error("private must still win over a visible project")
	}
	if !CanSeeView(priv, ownerVw) {
		t.Error("the creator should still see it")
	}
}

func Test_CanSeeDashboard(t *testing.T) {
	priv := Dashboard{Visibility: VisibilityPrivate, CreatedBy: owner}
	shared := Dashboard{Visibility: VisibilityOrg, CreatedBy: owner}

	if !CanSeeDashboard(priv, Viewer{UserID: owner}) || CanSeeDashboard(priv, Viewer{UserID: other}) {
		t.Error("private dashboards are creator-only")
	}
	if !CanSeeDashboard(shared, Viewer{UserID: other}) {
		t.Error("shared dashboards are readable by members")
	}
	// Dashboards are not project-scoped, so project visibility must not matter.
	if !CanSeeDashboard(shared, Viewer{UserID: other, VisibleProjects: nil}) {
		t.Error("a dashboard must not require project visibility")
	}
}

func Test_CanModify(t *testing.T) {
	cases := []struct {
		name       string
		visibility string
		who        Viewer
		want       bool
	}{
		{"creator may modify their shared record", VisibilityOrg, Viewer{UserID: owner}, true},
		{"creator may modify their private record", VisibilityPrivate, Viewer{UserID: owner}, true},
		{"a plain member may not modify someone else's", VisibilityOrg, Viewer{UserID: other}, false},
		// So a team is not stuck with a colleague's stale dashboard.
		{"an org admin may modify a shared record", VisibilityOrg, Viewer{UserID: other, OrgAdmin: true}, true},
		// Being an admin is not a reason to reach into private work.
		{"an org admin may not modify a private record", VisibilityPrivate, Viewer{UserID: other, OrgAdmin: true}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanModify(owner, c.visibility, c.who); got != c.want {
				t.Errorf("CanModify = %v, want %v", got, c.want)
			}
		})
	}
}

func Test_ParseVisibility(t *testing.T) {
	// Absent means shareable: a saved view is useless to a team if the default
	// hides it.
	if got, err := ParseVisibility(""); err != nil || got != VisibilityOrg {
		t.Errorf("empty gave %q, %v; want org", got, err)
	}
	for _, v := range []string{VisibilityOrg, VisibilityPrivate} {
		if got, err := ParseVisibility(v); err != nil || got != v {
			t.Errorf("%q gave %q, %v", v, got, err)
		}
	}
	for _, bad := range []string{"public", "PRIVATE", "team", "0"} {
		if _, err := ParseVisibility(bad); !errors.Is(err, ErrBadVisibility) {
			t.Errorf("ParseVisibility(%q) err = %v, want ErrBadVisibility", bad, err)
		}
	}
}

func Test_validateName(t *testing.T) {
	if _, err := validateName("  "); !errors.Is(err, ErrNameRequired) {
		t.Errorf("blank name err = %v", err)
	}
	if got, err := validateName("  Errors last hour  "); err != nil || got != "Errors last hour" {
		t.Errorf("got %q, %v; want trimmed", got, err)
	}
	if _, err := validateName(strings.Repeat("x", MaxNameLen+1)); !errors.Is(err, ErrNameTooLong) {
		t.Errorf("over-long name err = %v", err)
	}
}

func Test_validateDefinition(t *testing.T) {
	t.Run("absent falls back", func(t *testing.T) {
		got, err := validateDefinition(nil, "{}", false)
		if err != nil || string(got) != "{}" {
			t.Errorf("got %q, %v", got, err)
		}
		got, err = validateDefinition(nil, "[]", true)
		if err != nil || string(got) != "[]" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("invalid JSON rejected", func(t *testing.T) {
		if _, err := validateDefinition(json.RawMessage(`{oops`), "{}", false); err == nil {
			t.Error("expected a JSON error")
		}
	})

	// Panels are a layout list; an object would break every consumer.
	t.Run("panels must be an array", func(t *testing.T) {
		if _, err := validateDefinition(json.RawMessage(`{"a":1}`), "[]", true); !errors.Is(err, ErrPanelsNotList) {
			t.Errorf("err = %v, want ErrPanelsNotList", err)
		}
		if _, err := validateDefinition(json.RawMessage(`[{"a":1}]`), "[]", true); err != nil {
			t.Errorf("a valid array was rejected: %v", err)
		}
	})

	// The definition is opaque, so size is the only thing worth policing.
	t.Run("oversized rejected", func(t *testing.T) {
		big := json.RawMessage(`{"k":"` + strings.Repeat("x", MaxDefinitionBytes) + `"}`)
		if _, err := validateDefinition(big, "{}", false); !errors.Is(err, ErrDefTooLarge) {
			t.Errorf("err = %v, want ErrDefTooLarge", err)
		}
	})

	t.Run("arbitrary valid JSON passes through untouched", func(t *testing.T) {
		in := json.RawMessage(`{"q":"boom","level":["ERROR"],"nested":{"a":[1,2]}}`)
		got, err := validateDefinition(in, "{}", false)
		if err != nil || string(got) != string(in) {
			t.Errorf("definition was altered: %q", got)
		}
	})
}
