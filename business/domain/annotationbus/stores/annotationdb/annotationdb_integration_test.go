package annotationdb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/domain/annotationbus/stores/annotationdb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

type harness struct {
	db      *dbtest.Database
	bus     *annotationbus.Business
	fixture dbtest.Fixture
	second  uuid.UUID
}

func newHarness(t *testing.T) harness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	second := uuid.New()
	if _, err := db.DB.Exec(`
		INSERT INTO projects (id, org_id, name, date_created, date_updated)
		VALUES ($1, $2, 'second', NOW(), NOW())`, second, f.OrgID); err != nil {
		t.Fatalf("second project: %v", err)
	}

	return harness{
		db:      db,
		bus:     annotationbus.NewBusiness(db.Log, annotationdb.NewStore(db.Log, db.DB)),
		fixture: f,
		second:  second,
	}
}

func (h harness) note(t *testing.T, projectID uuid.UUID, logID *uuid.UUID, ts time.Time, body string) annotationbus.Annotation {
	t.Helper()

	a, err := h.bus.Create(context.Background(), h.fixture.OrgID, h.fixture.UserID, annotationbus.NewAnnotation{
		ProjectID: projectID,
		LogID:     logID,
		TS:        ts,
		Body:      body,
	})
	if err != nil {
		t.Fatalf("create annotation: %v", err)
	}

	return a
}

// A note round-trips with its anchor intact, and its timestamp stays on the
// instant it was given rather than the instant it was written.
func Test_Annotation_Integration_RoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	logID := uuid.New()
	at := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Millisecond)

	created := h.note(t, h.fixture.ProjectID, &logID, at, "  this one broke checkout  ")

	got, err := h.bus.QueryByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("query by id: %v", err)
	}

	if got.Body != "this one broke checkout" {
		t.Errorf("body = %q, want it trimmed", got.Body)
	}
	if got.LogID == nil || *got.LogID != logID {
		t.Errorf("logId = %v, want %s", got.LogID, logID)
	}
	if !got.TS.Equal(at) {
		t.Errorf("ts = %v, want %v", got.TS, at)
	}
	if got.OrgID != h.fixture.OrgID || got.ProjectID != h.fixture.ProjectID {
		t.Errorf("note landed in the wrong tenant: org %s project %s", got.OrgID, got.ProjectID)
	}

	// Editing changes the text and nothing else.
	body := "this one broke checkout, fixed in 4.13"
	updated, err := h.bus.Update(ctx, got, annotationbus.UpdateAnnotation{Body: &body})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Body != body {
		t.Errorf("body = %q, want %q", updated.Body, body)
	}

	reread, err := h.bus.QueryByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Body != body {
		t.Errorf("the edit did not persist: %q", reread.Body)
	}
	if !reread.TS.Equal(at) || reread.LogID == nil || *reread.LogID != logID {
		t.Errorf("the anchor moved on edit: ts %v log %v", reread.TS, reread.LogID)
	}
	if !reread.DateUpdated.After(reread.DateCreated) && reread.DateUpdated.Equal(reread.DateCreated) {
		t.Errorf("date_updated was not advanced by the edit")
	}

	if err := h.bus.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := h.bus.QueryByID(ctx, created.ID); !errors.Is(err, annotationbus.ErrNotFound) {
		t.Errorf("after delete, query = %v, want ErrNotFound", err)
	}
}

// Listing is what both the log view and a chart overlay read, so each filter has
// to narrow correctly and the caller's project scope has to be a hard bound.
func Test_Annotation_Integration_Filters(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	logID := uuid.New()

	onLog := h.note(t, h.fixture.ProjectID, &logID, now.Add(-30*time.Minute), "on a log line")
	marker := h.note(t, h.fixture.ProjectID, nil, now.Add(-10*time.Minute), "deployed 4.12")
	old := h.note(t, h.fixture.ProjectID, nil, now.Add(-72*time.Hour), "last week")
	elsewhere := h.note(t, h.second, nil, now.Add(-5*time.Minute), "other project")

	all, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.fixture.ProjectID, h.second}})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("got %d notes, want 4", len(all))
	}
	// Newest first, so a timeline renders without re-sorting.
	if all[0].ID != elsewhere.ID || all[3].ID != old.ID {
		t.Errorf("notes are not ordered newest first")
	}

	oneProject, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.second}})
	if err != nil {
		t.Fatalf("query one project: %v", err)
	}
	if len(oneProject) != 1 || oneProject[0].ID != elsewhere.ID {
		t.Errorf("project filter returned %d notes, want only the second project's", len(oneProject))
	}

	byLog, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.fixture.ProjectID}, LogID: &logID})
	if err != nil {
		t.Fatalf("query by log: %v", err)
	}
	if len(byLog) != 1 || byLog[0].ID != onLog.ID {
		t.Errorf("log filter returned %d notes, want the one attached to that log", len(byLog))
	}

	from := now.Add(-time.Hour)
	window, err := h.bus.Query(ctx, annotationbus.Filter{
		ProjectIDs: []uuid.UUID{h.fixture.ProjectID},
		From:       &from,
		To:         &now,
	})
	if err != nil {
		t.Fatalf("query window: %v", err)
	}
	if len(window) != 2 {
		t.Errorf("window returned %d notes, want the two inside the last hour", len(window))
	}
	for _, n := range window {
		if n.ID == old.ID {
			t.Errorf("a note outside the window was returned")
		}
	}

	// The upper bound is exclusive, matching every other range in the API.
	to := marker.TS
	upTo, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.fixture.ProjectID}, To: &to})
	if err != nil {
		t.Fatalf("query exclusive bound: %v", err)
	}
	for _, n := range upTo {
		if n.ID == marker.ID {
			t.Errorf("to= included a note exactly on the bound")
		}
	}

	limited, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.fixture.ProjectID}, Limit: 1})
	if err != nil {
		t.Fatalf("query limited: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("limit=1 returned %d notes", len(limited))
	}

	// An empty project scope returns nothing rather than everything: a caller who
	// can read no project must not be handed the org's notes.
	none, err := h.bus.Query(ctx, annotationbus.Filter{})
	if err != nil {
		t.Fatalf("query with no scope: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("an empty project scope returned %d notes, want 0", len(none))
	}
}

// Notes belong to the project they annotate, so deleting it takes them with it
// rather than leaving notes pointing at nothing.
func Test_Annotation_Integration_ProjectDeleteCascades(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.note(t, h.second, nil, time.Now().UTC(), "goes away with the project")
	keep := h.note(t, h.fixture.ProjectID, nil, time.Now().UTC(), "stays")

	if _, err := h.db.DB.Exec(`DELETE FROM projects WHERE id = $1`, h.second); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	left, err := h.bus.Query(ctx, annotationbus.Filter{ProjectIDs: []uuid.UUID{h.fixture.ProjectID, h.second}})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(left) != 1 || left[0].ID != keep.ID {
		t.Errorf("got %d notes, want only the surviving project's", len(left))
	}
}

// A blank body is rejected by the database as well as the business layer, so no
// path can create an empty note.
func Test_Annotation_Integration_BlankBodyRejectedByTheDatabase(t *testing.T) {
	h := newHarness(t)

	_, err := h.db.DB.Exec(`
		INSERT INTO log_annotations (org_id, project_id, ts, body, created_by)
		VALUES ($1, $2, NOW(), '   ', $3)`,
		h.fixture.OrgID, h.fixture.ProjectID, h.fixture.UserID)
	if err == nil {
		t.Fatalf("the database accepted a whitespace-only body")
	}
}
