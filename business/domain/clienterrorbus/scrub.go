package clienterrorbus

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Redacted is what a scrubbed secret is replaced with.
const Redacted = "[redacted]"

// Patterns for the things that must never reach storage.
//
// The client scrubs too, but the client is the thing that is broken — an error
// report arrives precisely when the page is not behaving as written, and a
// buggy build is entirely capable of sending a token it meant to strip. So this
// runs server-side as well, and it is the copy that decides what is stored.
var (
	// A query string. Stripped whole rather than key by key: a redacted
	// parameter list still tells us the shape of a URL nobody asked to keep.
	queryString = regexp.MustCompile(`\?[^\s"']*`)

	// Email addresses. The frontend is not supposed to send one anywhere, so any
	// occurrence is a leak by definition.
	email = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// Bearer tokens, cookie headers and our own key schemes, in whatever field
	// they were pasted into.
	bearer     = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]+`)
	cookieish  = regexp.MustCompile(`(?i)(cookie|set-cookie|authorization)\s*[:=]\s*[^\s;,"']+`)
	ourKeys    = regexp.MustCompile(`\bls_[a-z]+_live_[a-f0-9]+`)
	keyValue   = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|pwd)["']?\s*[:=]\s*["']?[^\s,;&"']{6,}`)
	jwtLooking = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]+`)
)

// ScrubText removes secrets and query strings from a free-text field.
func ScrubText(s string) string {
	if s == "" {
		return s
	}

	for _, p := range []*regexp.Regexp{jwtLooking, bearer, cookieish, ourKeys, keyValue, email} {
		s = p.ReplaceAllString(s, Redacted)
	}

	return s
}

// ScrubURL reduces a reported location to a path. The client is asked to send a
// path only; this enforces it, because a URL is where session tokens end up in
// practice.
func ScrubURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Drop a fragment before the query: a SPA can carry either.
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = queryString.ReplaceAllString(s, "")

	return ScrubText(s)
}

// truncate cuts s to at most n bytes without splitting a rune, marking that it
// was cut so a reader does not mistake a truncated stack for a shallow one.
//
// The cut has to land on a complete rune, not merely on a leading byte:
// Postgres rejects invalid UTF-8 outright, so a stack ending mid-character
// would fail the insert for the whole batch.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	cut := s[:n]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}

	return cut + "…[truncated]"
}

// Scrub returns a copy of the event with every field cleaned and bounded. It is
// the only way an event should reach the store.
func (ne NewEvent) Scrub() NewEvent {
	ne.Name = truncate(strings.TrimSpace(ne.Name), 200)
	ne.Message = truncate(ScrubText(ne.Message), MaxMessageLen)
	ne.Stack = truncate(ScrubText(ne.Stack), MaxStackLen)
	ne.ComponentStack = truncate(ScrubText(ne.ComponentStack), MaxComponentStackLen)
	ne.Release = truncate(strings.TrimSpace(ne.Release), MaxReleaseLen)
	ne.Environment = truncate(strings.TrimSpace(ne.Environment), 60)
	ne.URL = truncate(ScrubURL(ne.URL), MaxURLLen)
	ne.UserAgent = truncate(strings.TrimSpace(ne.UserAgent), MaxUserAgentLen)

	if ne.API != nil {
		api := *ne.API
		api.Method = truncate(strings.TrimSpace(api.Method), 10)
		api.Path = truncate(ScrubURL(api.Path), 300)
		api.Code = truncate(ScrubText(api.Code), 60)
		ne.API = &api
	}

	if len(ne.Breadcrumbs) > MaxBreadcrumbs {
		// Keep the most recent: the last thing that happened is the one that
		// explains the crash.
		ne.Breadcrumbs = ne.Breadcrumbs[len(ne.Breadcrumbs)-MaxBreadcrumbs:]
	}

	crumbs := make([]Breadcrumb, 0, len(ne.Breadcrumbs))
	for _, c := range ne.Breadcrumbs {
		crumbs = append(crumbs, Breadcrumb{
			TS:       c.TS,
			Category: truncate(strings.TrimSpace(c.Category), 40),
			Message:  truncate(ScrubURL(ScrubText(c.Message)), MaxBreadcrumbLen),
		})
	}
	ne.Breadcrumbs = crumbs

	if ne.SampledCount < 1 {
		ne.SampledCount = 1
	}

	return ne
}
