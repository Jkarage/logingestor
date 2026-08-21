package clienterrorbus

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// FingerprintVersion is stamped on every event so a later pass can tell which
// events were grouped by which algorithm.
//
// It will be bumped when source maps land: today's frames are minified, so a
// fingerprint built from them changes at every deploy — the same bug in
// index-64s.js and index-9f2.js looks like two issues. Version 1 accepts that,
// because grouping by minified frames is still far better than grouping by
// nothing, and the re-group is a background job over stored events rather than
// data we have to collect again.
const FingerprintVersion = 1

// maxFrames is how many frames take part in a fingerprint. Deep frames are the
// generic ones — the framework's own dispatch — and including them makes two
// unrelated bugs look alike.
const maxFrames = 5

// Patterns that reduce a message to a template. The aim is that "user 41 not
// found" and "user 9137 not found" are one issue, while genuinely different
// messages stay apart.
var (
	uuidLike = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	urlLike  = regexp.MustCompile(`https?://[^\s"')]+`)
	hexLike  = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	// A digit run, optionally with a short unit or hash suffix, so "30s" and
	// "45s" group — and so a bundle name like "index-64s" normalizes too, which
	// is what keeps the same message from splitting at every deploy.
	numberLike  = regexp.MustCompile(`\b\d[\d.,_]*[a-zA-Z]{0,3}\b`)
	quotedLike  = regexp.MustCompile(`(['"\x60])(?:[^'"\x60]{0,200})(['"\x60])`)
	whitespaces = regexp.MustCompile(`\s+`)
)

// vendorMarkers identify frames that belong to somebody else's code. A crash is
// almost never a bug in React itself, so a vendor frame is noise for grouping —
// but it is kept in the stored stack, where a human may want it.
var vendorMarkers = []string{
	"node_modules",
	"/vendor",
	"vendor-",
	"vendor.",
	"chunk-vendors",
	"webpack/bootstrap",
	"react-dom",
	"scheduler.production",
}

// NormalizeMessage reduces a message to a grouping template.
func NormalizeMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}

	// Order matters: URLs and UUIDs before numbers, or the number rule shreds
	// them into pieces that no longer match each other.
	msg = urlLike.ReplaceAllString(msg, "<url>")
	msg = uuidLike.ReplaceAllString(msg, "<uuid>")
	msg = hexLike.ReplaceAllString(msg, "<hex>")
	msg = quotedLike.ReplaceAllString(msg, "<str>")
	msg = numberLike.ReplaceAllString(msg, "<n>")
	msg = whitespaces.ReplaceAllString(msg, " ")

	return truncate(strings.TrimSpace(msg), 300)
}

// frameLine matches the location part of a stack frame across the shapes
// browsers produce: "at fn (file:line:col)", "at file:line:col", and Firefox's
// "fn@file:line:col".
var frameLine = regexp.MustCompile(`(?:at\s+(?:(?P<fn1>[^\s(]+)\s+\()?|(?P<fn2>[^\s@]+)@)?(?P<loc>[^\s()]+?):(?P<line>\d+):(?P<col>\d+)\)?`)

// Frame is one parsed stack frame.
type Frame struct {
	Function string
	File     string
	Line     string
	Column   string

	// InApp is false for frames belonging to a dependency.
	InApp bool
}

// String renders a frame for a fingerprint. The column is deliberately left out:
// a whitespace change in the bundler's output moves every column on the line,
// and grouping must not split on that.
func (f Frame) String() string {
	return f.File + ":" + f.Line + ":" + f.Function
}

// ParseStack extracts frames from a browser stack trace, oldest last, in the
// order the browser reported them.
func ParseStack(stack string) []Frame {
	var frames []Frame

	for _, line := range strings.Split(stack, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		m := frameLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		names := frameLine.SubexpNames()
		f := Frame{InApp: true}
		for i, name := range names {
			switch name {
			case "fn1", "fn2":
				if m[i] != "" {
					f.Function = m[i]
				}
			case "loc":
				f.File = m[i]
			case "line":
				f.Line = m[i]
			case "col":
				f.Column = m[i]
			}
		}

		if f.File == "" {
			continue
		}

		lower := strings.ToLower(f.File + " " + f.Function)
		for _, marker := range vendorMarkers {
			if strings.Contains(lower, marker) {
				f.InApp = false
				break
			}
		}

		frames = append(frames, f)
	}

	return frames
}

// InAppFrames returns the frames belonging to our own code, falling back to
// every frame when none qualify — a stack made entirely of vendor frames still
// has to group as something, and dropping it would lose the event.
func InAppFrames(frames []Frame) []Frame {
	out := make([]Frame, 0, len(frames))
	for _, f := range frames {
		if f.InApp {
			out = append(out, f)
		}
	}

	if len(out) == 0 {
		return frames
	}

	return out
}

// Fingerprint returns the grouping key for an event, plus the title and culprit
// derived from the same material.
//
// The inputs are the error type, the kind of failure, the normalized message and
// the top in-app frames. Kind is included because an api failure and a render
// crash with the same message are different problems with different owners.
func Fingerprint(ne NewEvent) (fingerprint, title, culprit string) {
	normalized := NormalizeMessage(ne.Message)

	frames := InAppFrames(ParseStack(ne.Stack))
	if len(frames) > maxFrames {
		frames = frames[:maxFrames]
	}

	parts := make([]string, 0, len(frames)+5)
	parts = append(parts, ne.Kind, ne.Name)

	// An api failure is identified by the request, not by the message. The stack
	// is our own fetch wrapper on every occurrence, and the message is generated
	// by that wrapper — "failed with 503" and "failed with 503 after 2 retries"
	// are one broken endpoint, and grouping them apart would turn one incident
	// into a list. The message still forms the title, so the issue reads well.
	if ne.Kind == KindAPI && ne.API != nil {
		parts = append(parts, ne.API.Method, ne.API.Path, strconv.Itoa(ne.API.Status), ne.API.Code)
	} else {
		parts = append(parts, normalized)
		for _, f := range frames {
			parts = append(parts, f.String())
		}
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	fingerprint = hex.EncodeToString(sum[:])

	title = ne.Name
	if normalized != "" {
		title = ne.Name + ": " + normalized
	}
	if title == "" {
		title = "Unknown error"
	}
	title = truncate(title, 300)

	switch {
	case len(frames) > 0:
		culprit = frames[0].Function
		if culprit == "" {
			culprit = frames[0].File
		} else {
			culprit += " (" + frames[0].File + ")"
		}
	case ne.Kind == KindAPI && ne.API != nil:
		culprit = strings.TrimSpace(ne.API.Method + " " + ne.API.Path)
	case ne.URL != "":
		culprit = ne.URL
	}

	return fingerprint, title, truncate(culprit, 300)
}
