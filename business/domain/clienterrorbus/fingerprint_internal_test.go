package clienterrorbus

import (
	"strings"
	"testing"
)

func Test_NormalizeMessage(t *testing.T) {
	// The point of normalisation is that these pairs group together.
	pairs := [][2]string{
		{"user 41 not found", "user 9137 not found"},
		{"failed to load /v1/orgs/1a2b3c4d-0000-0000-0000-000000000000/logs", "failed to load /v1/orgs/9f8e7d6c-1111-1111-1111-111111111111/logs"},
		{`Unexpected token "<" in JSON`, `Unexpected token "{" in JSON`},
		{"request to https://api.streamlogia.com/v1/logs failed", "request to https://api.streamlogia.com/v1/orgs failed"},
		{"chunk 4f3a9b2c1d8e7f60 failed", "chunk aabbccddeeff0011 failed"},
		{"timeout after 30s", "timeout after 45s"},
	}

	for _, p := range pairs {
		a, b := NormalizeMessage(p[0]), NormalizeMessage(p[1])
		if a != b {
			t.Errorf("these should group together:\n  %q -> %q\n  %q -> %q", p[0], a, p[1], b)
		}
	}

	// And that genuinely different messages stay apart.
	distinct := []string{
		"x is not a function",
		"Cannot read properties of undefined",
		"Network request failed",
	}
	seen := map[string]string{}
	for _, m := range distinct {
		n := NormalizeMessage(m)
		if prev, dup := seen[n]; dup {
			t.Errorf("%q and %q collapsed onto %q", prev, m, n)
		}
		seen[n] = m
	}

	if got := NormalizeMessage("   lots   of\n\tspace  "); got != "lots of space" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
}

func Test_ParseStack(t *testing.T) {
	// Chrome, Firefox and a bare location line, which is what a minified build
	// produces when the function name is gone.
	stack := `TypeError: x is not a function
    at a (https://streamlogia.com/assets/index-64s.js:1:24817)
    at onClick (https://streamlogia.com/assets/index-64s.js:1:31002)
    render@https://streamlogia.com/assets/vendor-8fa.js:2:9911
    at https://streamlogia.com/assets/index-64s.js:1:5
    at node_modules/react-dom/cjs/react-dom.production.min.js:120:14`

	frames := ParseStack(stack)
	if len(frames) != 5 {
		t.Fatalf("parsed %d frames, want 5: %+v", len(frames), frames)
	}

	if frames[0].Function != "a" || !strings.HasSuffix(frames[0].File, "index-64s.js") {
		t.Errorf("first frame = %+v", frames[0])
	}
	if frames[0].Line != "1" || frames[0].Column != "24817" {
		t.Errorf("first frame position = %s:%s", frames[0].Line, frames[0].Column)
	}
	if frames[2].Function != "render" {
		t.Errorf("firefox-style frame function = %q", frames[2].Function)
	}
	if frames[3].Function != "" {
		t.Errorf("anonymous frame got a function name: %q", frames[3].Function)
	}

	// Vendor frames are recognised but kept in the parse — they are dropped only
	// for grouping.
	if frames[2].InApp {
		t.Errorf("vendor-8fa.js was treated as in-app")
	}
	if frames[4].InApp {
		t.Errorf("a node_modules frame was treated as in-app")
	}

	inApp := InAppFrames(frames)
	if len(inApp) != 3 {
		t.Errorf("in-app frames = %d, want 3", len(inApp))
	}

	// A stack that is entirely vendor code still has to group as something.
	only := ParseStack("at node_modules/react-dom/x.js:1:1")
	if len(InAppFrames(only)) != 1 {
		t.Errorf("an all-vendor stack lost its frames")
	}

	// Free text with no frames must not panic or invent any.
	if got := ParseStack("something went wrong"); len(got) != 0 {
		t.Errorf("parsed frames out of prose: %+v", got)
	}
}

func Test_Fingerprint_GroupsAndSeparates(t *testing.T) {
	base := NewEvent{
		Kind:    KindReact,
		Name:    "TypeError",
		Message: "cannot read properties of undefined (reading 'id')",
		Stack: `TypeError: cannot read properties of undefined
    at LogsView (https://streamlogia.com/assets/index-64s.js:1:24817)
    at renderWithHooks (https://streamlogia.com/assets/vendor-8fa.js:2:9911)`,
	}

	fp, title, culprit := Fingerprint(base)
	if len(fp) != 64 {
		t.Fatalf("fingerprint is %d characters, want a sha256 hex", len(fp))
	}
	if !strings.HasPrefix(title, "TypeError: ") {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(culprit, "LogsView") {
		t.Errorf("culprit = %q, want the top in-app frame", culprit)
	}

	// The same bug reported with a different variable value groups together.
	same := base
	same.Message = "cannot read properties of undefined (reading 'name')"
	if got, _, _ := Fingerprint(same); got != fp {
		t.Errorf("a quoted-value difference split the issue")
	}

	// A column shift from a bundler whitespace change must not split it either.
	shifted := base
	shifted.Stack = strings.ReplaceAll(base.Stack, ":1:24817", ":1:24999")
	if got, _, _ := Fingerprint(shifted); got != fp {
		t.Errorf("a column change split the issue")
	}

	// A different throw site is a different issue.
	elsewhere := base
	elsewhere.Stack = strings.ReplaceAll(base.Stack, "LogsView", "SourcesView")
	if got, _, _ := Fingerprint(elsewhere); got == fp {
		t.Errorf("two different call sites share a fingerprint")
	}

	// So is the same message from a different kind of failure: an api error and a
	// render crash have different owners.
	otherKind := base
	otherKind.Kind = KindUnhandled
	if got, _, _ := Fingerprint(otherKind); got == fp {
		t.Errorf("kind is not part of the fingerprint")
	}

	// And a different error type.
	otherName := base
	otherName.Name = "RangeError"
	if got, _, _ := Fingerprint(otherName); got == fp {
		t.Errorf("error name is not part of the fingerprint")
	}
}

// An api error's stack is always our own fetch wrapper, so the request has to
// identify it or every failing endpoint becomes one issue.
func Test_Fingerprint_APIErrorsSplitByRequest(t *testing.T) {
	base := NewEvent{
		Kind:    KindAPI,
		Name:    "ApiError",
		Message: "request failed with 503",
		Stack:   "at request (https://streamlogia.com/assets/index-64s.js:1:900)",
		API:     &APIContext{Method: "POST", Path: "/v1/orgs/:id/logs", Status: 503, Code: "upstream_error"},
	}

	fp, _, culprit := Fingerprint(base)
	if !strings.Contains(culprit, "/v1/orgs/:id/logs") && !strings.Contains(culprit, "request") {
		t.Errorf("culprit = %q", culprit)
	}

	other := base
	other.API = &APIContext{Method: "GET", Path: "/v1/orgs/:id/members", Status: 503, Code: "upstream_error"}
	if got, _, _ := Fingerprint(other); got == fp {
		t.Errorf("two different endpoints share a fingerprint")
	}

	// The same endpoint failing the same way is one issue, whatever the message
	// happened to interpolate.
	same := base
	same.Message = "request failed with 503 after 2 retries"
	if got, _, _ := Fingerprint(same); got != fp {
		t.Errorf("the same endpoint split into two issues")
	}
}

// A report with nothing but a message still has to produce a usable issue.
func Test_Fingerprint_Degrades(t *testing.T) {
	fp, title, culprit := Fingerprint(NewEvent{Kind: KindManual, Name: "Error", Message: "boom", URL: "/dashboard"})

	if len(fp) != 64 {
		t.Errorf("no fingerprint for a stackless event")
	}
	if title != "Error: boom" {
		t.Errorf("title = %q", title)
	}
	if culprit != "/dashboard" {
		t.Errorf("culprit = %q, want the page it happened on", culprit)
	}

	// Not even a name.
	_, title, _ = Fingerprint(NewEvent{Kind: KindManual, Message: ""})
	if title != "Unknown error" {
		t.Errorf("title = %q, want a placeholder", title)
	}
}
