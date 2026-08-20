package viewdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/business/domain/viewbus/stores/viewdb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

func newBus(t *testing.T) (*viewbus.Business, *dbtest.Database, dbtest.Fixture) {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	return viewbus.NewBusiness(db.Log, viewdb.NewStore(db.Log, db.DB)), db, f
}

// A permalink is created once and resolved by slug from then on, which is the
// only lookup the URL can perform.
func Test_Permalink_Integration_ResolveBySlug(t *testing.T) {
	bus, _, f := newBus(t)
	ctx := context.Background()

	logID := uuid.New()

	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{
		Kind:      viewbus.PermalinkLog,
		ProjectID: &f.ProjectID,
		LogID:     &logID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if link.Slug == "" {
		t.Fatalf("created permalink has no slug")
	}

	got, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, link.Slug)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ID != link.ID || got.Kind != viewbus.PermalinkLog {
		t.Errorf("resolved the wrong permalink: %s kind %s", got.ID, got.Kind)
	}
	if got.LogID == nil || *got.LogID != logID {
		t.Errorf("logId = %v, want %s", got.LogID, logID)
	}
	if got.ProjectID == nil || *got.ProjectID != f.ProjectID {
		t.Errorf("projectId = %v, want %s", got.ProjectID, f.ProjectID)
	}

	if _, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, "nope-nope-nope"); !errors.Is(err, viewbus.ErrNotFound) {
		t.Errorf("an unknown slug = %v, want ErrNotFound", err)
	}
}

// A query permalink freezes the filters as they were, so a link shared today
// still opens the same window next week.
func Test_Permalink_Integration_QueryRoundTrips(t *testing.T) {
	bus, _, f := newBus(t)
	ctx := context.Background()

	query := json.RawMessage(`{"q":"timeout","level":["ERROR"],"from":"2026-08-01T00:00:00Z","to":"2026-08-02T00:00:00Z"}`)

	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{
		Kind:  viewbus.PermalinkQuery,
		Query: query,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, link.Slug)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var want, have map[string]any
	if err := json.Unmarshal(query, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(got.Query, &have); err != nil {
		t.Fatalf("stored query is not valid JSON: %v", err)
	}
	if len(have) != len(want) || have["q"] != want["q"] || have["from"] != want["from"] {
		t.Errorf("query did not round-trip: %s", got.Query)
	}
	if got.LogID != nil {
		t.Errorf("a query permalink came back carrying a log id")
	}
}

// The slug is unguessable but it is not authorization: resolving one is scoped to
// the org in the path, so a slug lifted from another tenant reads as missing.
func Test_Permalink_Integration_ScopedToItsOrg(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{Kind: viewbus.PermalinkQuery})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	other := db.SeedFixture(t, "free")
	if _, err := bus.QueryPermalinkBySlug(ctx, other.OrgID, link.Slug); !errors.Is(err, viewbus.ErrNotFound) {
		t.Errorf("another org resolved the slug: %v", err)
	}

	links, err := bus.QueryPermalinksByOrg(ctx, other.OrgID)
	if err != nil {
		t.Fatalf("list other org: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("another org listed %d permalinks, want 0", len(links))
	}
}

// Revoking a link stops it resolving, which is the only way to un-share a URL
// that has already been pasted somewhere.
func Test_Permalink_Integration_DeleteRevokesTheLink(t *testing.T) {
	bus, _, f := newBus(t)
	ctx := context.Background()

	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{Kind: viewbus.PermalinkQuery})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := bus.DeletePermalink(ctx, link.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, link.Slug); !errors.Is(err, viewbus.ErrNotFound) {
		t.Errorf("a revoked link still resolves: %v", err)
	}
}

// Retention deletes logs. A permalink to a purged log must still resolve, so the
// client can say the log has aged out instead of the link appearing broken.
func Test_Permalink_Integration_SurvivesAPurgedLog(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	logID := uuid.New()
	if _, err := db.DB.Exec(`
		INSERT INTO logs (id, project_id, level, message, source, source_type, ts)
		VALUES ($1, $2, 'ERROR', 'boom', 'api', 'app', NOW())`, logID, f.ProjectID); err != nil {
		t.Fatalf("insert log: %v", err)
	}

	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{
		Kind:      viewbus.PermalinkLog,
		ProjectID: &f.ProjectID,
		LogID:     &logID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := db.DB.Exec(`DELETE FROM logs WHERE id = $1`, logID); err != nil {
		t.Fatalf("purge log: %v", err)
	}

	got, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, link.Slug)
	if err != nil {
		t.Fatalf("a permalink to a purged log stopped resolving: %v", err)
	}
	if got.LogID == nil || *got.LogID != logID {
		t.Errorf("logId = %v, want the purged log's id %s", got.LogID, logID)
	}
}

// A log permalink belongs to its project, so deleting the project revokes the
// link rather than leaving a pointer into a deleted tenant.
func Test_Permalink_Integration_ProjectDeleteCascades(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	logID := uuid.New()
	link, err := bus.CreatePermalink(ctx, f.OrgID, f.UserID, viewbus.NewPermalink{
		Kind:      viewbus.PermalinkLog,
		ProjectID: &f.ProjectID,
		LogID:     &logID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := db.DB.Exec(`DELETE FROM projects WHERE id = $1`, f.ProjectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	if _, err := bus.QueryPermalinkBySlug(ctx, f.OrgID, link.Slug); !errors.Is(err, viewbus.ErrNotFound) {
		t.Errorf("a link into a deleted project still resolves: %v", err)
	}
}

// The database is the authority on slug uniqueness, and a collision must be
// retried rather than surfaced.
func Test_Permalink_Integration_SlugIsUnique(t *testing.T) {
	_, db, f := newBus(t)

	slug, err := viewbus.GenerateSlug()
	if err != nil {
		t.Fatalf("generate slug: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, err = db.DB.Exec(`
			INSERT INTO log_permalinks (org_id, slug, kind, query, created_by)
			VALUES ($1, $2, 'query', '{}', $3)`, f.OrgID, slug, f.UserID)
		if i == 0 && err != nil {
			t.Fatalf("first insert: %v", err)
		}
	}

	if err == nil {
		t.Fatalf("the database accepted a duplicate slug")
	}
}
