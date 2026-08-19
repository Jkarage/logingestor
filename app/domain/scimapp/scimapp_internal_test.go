package scimapp

import (
	"encoding/json"
	"testing"
)

func Test_parseUserNameEq(t *testing.T) {
	ok := map[string]string{
		`userName eq "person@example.com"`: "person@example.com",
		`userName Eq "Person@Example.com"`: "person@example.com",
		`  userName eq "a@b.c"  `:          "a@b.c",
		`username eq "a@b.c"`:              "a@b.c",
		`userName eq person@example.com`:   "person@example.com",
	}
	for filter, want := range ok {
		got, matched := parseUserNameEq(filter)
		if !matched {
			t.Errorf("parseUserNameEq(%q) did not match", filter)
			continue
		}
		if got != want {
			t.Errorf("parseUserNameEq(%q) = %q, want %q", filter, got, want)
		}
	}

	// Anything else must be refused rather than silently ignored: a filter we
	// quietly drop makes the agent think the user is absent and create a duplicate.
	for _, bad := range []string{
		``,
		`displayName eq "x"`,
		`userName co "x"`,
		`userName eq ""`,
		`active eq true`,
		`userName`,
	} {
		if v, matched := parseUserNameEq(bad); matched {
			t.Errorf("parseUserNameEq(%q) matched with %q, want refusal", bad, v)
		}
	}
}

func Test_activeFromPatch(t *testing.T) {
	patch := func(body string) PatchRequest {
		var p PatchRequest
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		return p
	}

	// The shape Okta and Entra send on offboarding.
	t.Run("replace active false", func(t *testing.T) {
		active, found := activeFromPatch(patch(`{"Operations":[{"op":"replace","path":"active","value":false}]}`))
		if !found || active {
			t.Errorf("active=%v found=%v, want false/true", active, found)
		}
	})

	t.Run("replace active true", func(t *testing.T) {
		active, found := activeFromPatch(patch(`{"Operations":[{"op":"replace","path":"active","value":true}]}`))
		if !found || !active {
			t.Errorf("active=%v found=%v, want true/true", active, found)
		}
	})

	// Some IdPs send active as a string.
	t.Run("string valued active", func(t *testing.T) {
		active, found := activeFromPatch(patch(`{"Operations":[{"op":"replace","path":"active","value":"False"}]}`))
		if !found || active {
			t.Errorf("active=%v found=%v, want false/true", active, found)
		}
	})

	// Others replace the whole resource with no path.
	t.Run("pathless replace with active in the value object", func(t *testing.T) {
		active, found := activeFromPatch(patch(`{"Operations":[{"op":"replace","value":{"active":false,"userName":"a@b.c"}}]}`))
		if !found || active {
			t.Errorf("active=%v found=%v, want false/true", active, found)
		}
	})

	t.Run("case-insensitive op", func(t *testing.T) {
		if _, found := activeFromPatch(patch(`{"Operations":[{"op":"Replace","path":"active","value":false}]}`)); !found {
			t.Error("op matching must be case-insensitive")
		}
	})

	// Nothing actionable: the handler reports current state rather than erroring,
	// so an agent syncing attributes we do not store still succeeds.
	for name, body := range map[string]string{
		"no operations":      `{"Operations":[]}`,
		"unrelated path":     `{"Operations":[{"op":"replace","path":"displayName","value":"X"}]}`,
		"remove op ignored":  `{"Operations":[{"op":"remove","path":"active"}]}`,
		"non-boolean active": `{"Operations":[{"op":"replace","path":"active","value":42}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, found := activeFromPatch(patch(body)); found {
				t.Errorf("%s should not yield an active value", name)
			}
		})
	}
}

func Test_User_PrimaryEmail(t *testing.T) {
	cases := []struct {
		name string
		user User
		want string
	}{
		{
			name: "primary entry wins",
			user: User{UserName: "login@x.com", Emails: []Email{
				{Value: "secondary@x.com"}, {Value: "Primary@X.com", Primary: true},
			}},
			want: "primary@x.com",
		},
		{
			name: "first entry when none is primary",
			user: User{UserName: "login@x.com", Emails: []Email{{Value: "First@X.com"}}},
			want: "first@x.com",
		},
		{
			// Most IdPs put the email in userName and send no emails array.
			name: "falls back to userName",
			user: User{UserName: "Login@X.com"},
			want: "login@x.com",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.user.PrimaryEmail(); got != c.want {
				t.Errorf("PrimaryEmail() = %q, want %q", got, c.want)
			}
		})
	}
}

func Test_User_DisplayName(t *testing.T) {
	if got := (User{UserName: "a@b.c", Name: &Name{Formatted: "Ada Lovelace"}}).DisplayName(); got != "Ada Lovelace" {
		t.Errorf("got %q", got)
	}
	if got := (User{UserName: "a@b.c", Name: &Name{GivenName: "Ada", FamilyName: "Lovelace"}}).DisplayName(); got != "Ada Lovelace" {
		t.Errorf("got %q", got)
	}
	if got := (User{UserName: "a@b.c", Name: &Name{}}).DisplayName(); got != "a@b.c" {
		t.Errorf("got %q", got)
	}
	if got := (User{UserName: "a@b.c"}).DisplayName(); got != "a@b.c" {
		t.Errorf("got %q", got)
	}
}

func Test_scimErr_ShapeAndStatus(t *testing.T) {
	e := scimErr(404, "", "user not found")

	if e.HTTPStatus() != 404 {
		t.Errorf("HTTPStatus() = %d, want 404", e.HTTPStatus())
	}

	data, ct, err := e.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// SCIM clients parse this content type and schema, not our normal envelope.
	if ct != "application/scim+json" {
		t.Errorf("content type = %q", ct)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["status"] != "404" {
		t.Errorf("status = %v, want the string \"404\"", out["status"])
	}
	if schemas, ok := out["schemas"].([]any); !ok || len(schemas) != 1 || schemas[0] != schemaError {
		t.Errorf("schemas = %v", out["schemas"])
	}
}

func Test_created201_And_noContent(t *testing.T) {
	c := created201{}
	if c.HTTPStatus() != 201 {
		t.Error("SCIM create must return 201")
	}
	n := noContent{}
	if n.HTTPStatus() != 204 {
		t.Error("SCIM delete must return 204")
	}
}

func Test_randomPassword_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 30 {
		pw, err := randomPassword()
		if err != nil {
			t.Fatalf("randomPassword: %v", err)
		}
		if seen[pw.String()] {
			t.Fatal("duplicate generated password")
		}
		seen[pw.String()] = true
	}
}
