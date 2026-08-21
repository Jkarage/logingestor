package clienterrorbus

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The client is asked to strip these, but the client is the thing that is
// broken — a report arrives precisely because the page is not doing what it was
// written to do. Everything below must be caught server-side too.
func Test_ScrubText_RemovesSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone string
	}{
		{"bearer token", "fetch failed: Authorization: Bearer abc123def456ghi", "abc123def456ghi"},
		{"raw bearer", "sent bearer eyJhbGciOiJSUzI1NiJ9x", "eyJhbGciOiJSUzI1NiJ9x"},
		{"jwt in a message", "bad token eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk", "eyJzdWIiOiIxMjM0NTY3ODkwIn0"},
		{"cookie header", "Cookie: session=deadbeefcafe", "deadbeefcafe"},
		{"our own ingest key", "used ls_src_live_a1b2c3d4e5f6", "ls_src_live_a1b2c3d4e5f6"},
		{"api key assignment", `config {"apiKey": "sk_live_9f8e7d6c5b4a"}`, "sk_live_9f8e7d6c5b4a"},
		{"password field", "password=hunter2hunter2", "hunter2hunter2"},
		{"an email address", "failed for joseph@bsa.ai on save", "joseph@bsa.ai"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScrubText(c.in)

			if strings.Contains(got, c.gone) {
				t.Errorf("secret survived scrubbing: %q", got)
			}
			if !strings.Contains(got, Redacted) {
				t.Errorf("nothing was redacted: %q", got)
			}
		})
	}

	// Ordinary text is left alone: over-scrubbing makes an issue unreadable.
	plain := "cannot read properties of undefined (reading 'id')"
	if got := ScrubText(plain); got != plain {
		t.Errorf("plain message was altered: %q", got)
	}
}

func Test_ScrubURL(t *testing.T) {
	cases := [][2]string{
		{"/dashboard/logs?token=abc123&level=ERROR", "/dashboard/logs"},
		{"/dashboard/logs#section?token=abc", "/dashboard/logs"},
		{"/orgs/1a2b/settings", "/orgs/1a2b/settings"},
		{"", ""},
	}

	for _, c := range cases {
		if got := ScrubURL(c[0]); got != c[1] {
			t.Errorf("ScrubURL(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// Every field an anonymous caller controls has to be bounded, or one report can
// carry a megabyte.
func Test_Scrub_BoundsEveryField(t *testing.T) {
	long := strings.Repeat("x", 50_000)

	crumbs := make([]Breadcrumb, 100)
	for i := range crumbs {
		crumbs[i] = Breadcrumb{Category: long, Message: long}
	}

	ne := NewEvent{
		Name:           long,
		Message:        long,
		Stack:          long,
		ComponentStack: long,
		Release:        long,
		Environment:    long,
		URL:            "/x?" + long,
		UserAgent:      long,
		API:            &APIContext{Method: long, Path: "/p?" + long, Code: long},
		Breadcrumbs:    crumbs,
		SampledCount:   0,
	}.Scrub()

	checks := []struct {
		name  string
		got   int
		limit int
	}{
		{"message", len(ne.Message), MaxMessageLen},
		{"stack", len(ne.Stack), MaxStackLen},
		{"componentStack", len(ne.ComponentStack), MaxComponentStackLen},
		{"release", len(ne.Release), MaxReleaseLen},
		{"url", len(ne.URL), MaxURLLen},
		{"userAgent", len(ne.UserAgent), MaxUserAgentLen},
	}
	for _, c := range checks {
		// The truncation marker is added after the cut, so allow for it.
		if c.got > c.limit+16 {
			t.Errorf("%s is %d bytes, limit is %d", c.name, c.got, c.limit)
		}
	}

	if len(ne.Breadcrumbs) != MaxBreadcrumbs {
		t.Errorf("breadcrumbs = %d, want %d", len(ne.Breadcrumbs), MaxBreadcrumbs)
	}
	for _, c := range ne.Breadcrumbs {
		if len(c.Message) > MaxBreadcrumbLen+16 {
			t.Errorf("breadcrumb message is %d bytes", len(c.Message))
		}
	}

	// A sampled count below one would let a client subtract from the totals.
	if ne.SampledCount != 1 {
		t.Errorf("sampledCount = %d, want 1", ne.SampledCount)
	}

	// Postgres rejects invalid UTF-8, so a cut through a multi-byte character
	// would fail the insert for the whole batch.
	multi := NewEvent{Message: strings.Repeat("日", MaxMessageLen)}.Scrub()
	if !utf8.ValidString(multi.Message) {
		t.Errorf("truncation produced invalid UTF-8")
	}
}

// The keys the browser sends are hints. Scrubbing must not silently keep a
// query string that the client forgot to strip out of a breadcrumb.
func Test_Scrub_BreadcrumbsAreCleaned(t *testing.T) {
	ne := NewEvent{
		Breadcrumbs: []Breadcrumb{
			{Category: "fetch", Message: "GET /v1/logs?token=abc123def456 200"},
			{Category: "navigation", Message: "/login -> /dashboard"},
		},
	}.Scrub()

	if strings.Contains(ne.Breadcrumbs[0].Message, "abc123def456") {
		t.Errorf("breadcrumb kept a token: %q", ne.Breadcrumbs[0].Message)
	}
	if ne.Breadcrumbs[1].Message != "/login -> /dashboard" {
		t.Errorf("plain breadcrumb was altered: %q", ne.Breadcrumbs[1].Message)
	}
}
