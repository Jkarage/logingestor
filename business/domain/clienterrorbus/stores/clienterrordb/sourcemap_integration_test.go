package clienterrordb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/sdk/retention"
)

// sourceMapJSON is a hand-written v3 map for index-64s.js. Two segments on
// generated line 1, encoded as base64 VLQ deltas:
//
//	column 0  -> src/views/LogsView.jsx        142:8, handleFilterChange
//	column 20 -> src/components/LevelPicker.jsx 32:14, onChange
//
// sourcesContent is present on purpose: the upload must strip it.
const sourceMapJSON = `{
	"version": 3,
	"file": "index-64s.js",
	"sources": ["src/views/LogsView.jsx", "src/components/LevelPicker.jsx"],
	"sourcesContent": ["export function handleFilterChange() { /* the real source */ }", "export function onChange() {}"],
	"names": ["handleFilterChange", "onChange"],
	"mappings": "AA6IQA,oBC9GMC"
}`

// minifiedCrash is the same bug as it arrives from production: one line, renamed
// functions, a column offset.
func minifiedCrash(bundle, release string) clienterrorbus.NewEvent {
	return clienterrorbus.NewEvent{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Level:      clienterrorbus.LevelError,
		Kind:       clienterrorbus.KindReact,
		Name:       "TypeError",
		Message:    "x is not a function",
		Stack:      "TypeError: x is not a function\n    at a (https://streamlogia.com/assets/" + bundle + ":1:1)",
		Release:    release,
		URL:        "/dashboard/logs",
	}
}

// The point of the feature: an issue filed from a minified stack is re-keyed and
// made readable once CI uploads the map, without re-collecting anything.
func Test_SourceMap_Integration_LateUploadRegroups(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}
	const release = "streamlogia-frontend@1.5.0"

	// A crash arrives before the maps do, which is the race CI always loses at
	// least once.
	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{minifiedCrash("index-64s.js", release)}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	before, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one issue, got %d (err %v)", len(before), err)
	}
	if !strings.Contains(before[0].Culprit, "index-64s.js") {
		t.Errorf("culprit = %q, want the minified frame", before[0].Culprit)
	}

	// The notifier saw a genuinely new issue.
	if len(h.notes.opened) != 1 {
		t.Fatalf("notified %d new issues, want 1", len(h.notes.opened))
	}

	// CI uploads the map.
	stored, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
		Release:  release,
		FileName: "dist/assets/index-64s.js.map",
		Content:  []byte(sourceMapJSON),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if stored.FileName != "index-64s.js" {
		t.Errorf("stored as %q, want the generated file name a stack trace carries", stored.FileName)
	}

	// The map is stored compressed and without the original source in it.
	var raw []byte
	if err := h.db.DB.Get(&raw, `SELECT content FROM client_error_artifacts WHERE release = $1`, release); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if strings.Contains(string(raw), "the real source") {
		t.Errorf("sourcesContent was stored — we are holding the frontend source at rest")
	}
	if len(raw) >= len(sourceMapJSON) {
		t.Errorf("stored %d bytes for a %d byte map; it was not compressed", len(raw), len(sourceMapJSON))
	}

	// The upload queued the existing event, and the worker re-keys it.
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("regroup: %v", err)
	}

	after, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query after regroup: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("got %d issues after re-grouping, want the minified one replaced: %+v", len(after), after)
	}
	if !strings.Contains(after[0].Culprit, "handleFilterChange") {
		t.Errorf("culprit = %q, want the original function name", after[0].Culprit)
	}
	if after[0].ID == before[0].ID {
		t.Errorf("the issue kept its old fingerprint; nothing was re-keyed")
	}
	if after[0].EventCount != 1 {
		t.Errorf("eventCount = %d, want 1", after[0].EventCount)
	}

	// Re-keying is not a new crash. Paging the team for every issue a map upload
	// touches is how a feature gets turned off.
	if len(h.notes.opened) != 1 {
		t.Errorf("a re-group notified %d new issues, want 0 beyond the original", len(h.notes.opened)-1)
	}

	// The de-minified stack is stored, and the raw one is kept beside it.
	var event struct {
		Stack    string  `db:"stack"`
		Resolved *string `db:"resolved_stack"`
		Version  int     `db:"fingerprint_version"`
	}
	if err := h.db.DB.Get(&event, `
		SELECT stack, resolved_stack, fingerprint_version FROM client_error_events LIMIT 1`); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if event.Resolved == nil || !strings.Contains(*event.Resolved, "LogsView.jsx:142") {
		t.Errorf("resolved stack = %v", event.Resolved)
	}
	if !strings.Contains(event.Stack, "index-64s.js:1:1") {
		t.Errorf("the raw stack was overwritten: %q", event.Stack)
	}
	if event.Version != clienterrorbus.ResolvedFingerprintVersion {
		t.Errorf("fingerprint version = %d, want %d", event.Version, clienterrorbus.ResolvedFingerprintVersion)
	}
}

// With maps in place before the crash, the same bug across two deploys is one
// issue instead of one per release — which is the reason this matters more for
// grouping than for readability.
func Test_SourceMap_Integration_OneIssueAcrossReleases(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}

	// Two releases, two bundle names, same map contents.
	for _, r := range []struct{ release, bundle string }{
		{"app@1.0.0", "index-aaa.js"},
		{"app@1.1.0", "index-bbb.js"},
	} {
		if _, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
			Release:  r.release,
			FileName: r.bundle + ".map",
			Content:  []byte(strings.Replace(sourceMapJSON, "index-64s.js", r.bundle, 1)),
		}); err != nil {
			t.Fatalf("upload %s: %v", r.release, err)
		}

		if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{minifiedCrash(r.bundle, r.release)}); err != nil {
			t.Fatalf("ingest %s: %v", r.release, err)
		}
		if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
			t.Fatalf("process %s: %v", r.release, err)
		}
	}

	issues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want one across both releases: %+v", len(issues), issues)
	}
	if issues[0].EventCount != 2 {
		t.Errorf("eventCount = %d, want 2", issues[0].EventCount)
	}
	if len(issues[0].Releases) != 2 {
		t.Errorf("releases = %v, want both", issues[0].Releases)
	}
}

// An upload has to be idempotent: a deploy job re-runs, and CI sends the same
// files again.
func Test_SourceMap_Integration_UploadIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
			Release:  "app@2.0.0",
			FileName: "index-ccc.js.map",
			Content:  []byte(strings.Replace(sourceMapJSON, "index-64s.js", "index-ccc.js", 1)),
		}); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}

	stored, err := h.bus.QueryArtifacts(ctx, "app@2.0.0")
	if err != nil {
		t.Fatalf("query artifacts: %v", err)
	}
	if len(stored) != 1 {
		t.Errorf("stored %d artifacts for three uploads of one file", len(stored))
	}
}

// A file that is not a source map must be refused, so a mistake in CI fails at
// the upload rather than silently storing junk the resolver will never use.
func Test_SourceMap_Integration_RejectsNonMaps(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		content string
	}{
		{"the bundle instead of the map", "!function(){var a=1}();"},
		{"json with no mappings", `{"version":3,"sources":["a.js"]}`},
		{"json with no version", `{"mappings":"AAAA","sources":["a.js"]}`},
		{"empty", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
				Release: "app@3.0.0", FileName: "x.js.map", Content: []byte(c.content),
			})
			if err == nil {
				t.Fatalf("accepted something that is not a source map")
			}
		})
	}

	if stored, _ := h.bus.QueryArtifacts(ctx, "app@3.0.0"); len(stored) != 0 {
		t.Errorf("%d rejected uploads were stored anyway", len(stored))
	}
}

// Maps for a release nothing refers to are dead weight, but not before the
// deploy they were uploaded for has had a chance to happen.
func Test_SourceMap_Integration_RetentionPrunesOrphans(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	upload := func(release string) {
		t.Helper()
		if _, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
			Release: release, FileName: "index-ddd.js.map", Content: []byte(sourceMapJSON),
		}); err != nil {
			t.Fatalf("upload %s: %v", release, err)
		}
	}

	upload("app@fresh")     // just uploaded, no events yet — the deploy is in flight
	upload("app@abandoned") // uploaded long ago, no events ever arrived
	upload("app@live")      // has events

	if _, err := h.db.DB.Exec(`
		UPDATE client_error_artifacts SET date_created = NOW() - interval '30 days'
		WHERE release = 'app@abandoned'`); err != nil {
		t.Fatalf("age the abandoned release: %v", err)
	}

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}
	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{minifiedCrash("index-ddd.js", "app@live")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if _, err := retention.Run(ctx, h.db.Log, h.db.DB, retention.Config{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	var releases []string
	if err := h.db.DB.Select(&releases, `SELECT release FROM client_error_artifacts ORDER BY release`); err != nil {
		t.Fatalf("read artifacts: %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("releases left = %v, want the fresh and the live ones", releases)
	}
	for _, r := range releases {
		if r == "app@abandoned" {
			t.Errorf("an orphaned map survived")
		}
	}
}

// The resolved stack has to reach the API, or the readability half of the
// feature is invisible: the dashboard would still be showing index-64s.js:1.
func Test_SourceMap_Integration_ResolvedStackIsReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	const release = "streamlogia-frontend@2.0.0"

	if _, err := h.bus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
		Release: release, FileName: "index-64s.js.map", Content: []byte(sourceMapJSON),
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}
	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{minifiedCrash("index-64s.js", release)}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	issues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected one issue, got %d (err %v)", len(issues), err)
	}

	events, err := h.bus.QueryIssueEvents(ctx, issues[0].ID, 10)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	e := events[0]
	if !strings.Contains(e.ResolvedStack, "LogsView.jsx:142") {
		t.Errorf("the event does not carry a resolved stack: %q", e.ResolvedStack)
	}
	if !strings.Contains(e.Stack, "index-64s.js") {
		t.Errorf("the raw stack was not kept: %q", e.Stack)
	}
}
