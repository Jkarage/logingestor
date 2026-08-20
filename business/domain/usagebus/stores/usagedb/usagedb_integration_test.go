package usagedb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/domain/usagebus/stores/usagedb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
	"github.com/jkarage/logingestor/business/sdk/retention"
)

type usageHarness struct {
	db       *dbtest.Database
	store    *usagedb.Store
	fixture  dbtest.Fixture
	sourceID uuid.UUID
}

func newUsageHarness(t *testing.T) usageHarness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	sourceID := uuid.New()
	if _, err := db.DB.Exec(`
		INSERT INTO sources (id, org_id, project_id, kind, name, key_prefix, key_hash)
		VALUES ($1, $2, $3, 'otel', 'collector', 'ls_src_live_abc', $4)`,
		sourceID, f.OrgID, f.ProjectID, sourceID.String()); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	return usageHarness{db: db, store: usagedb.NewStore(db.Log, db.DB), fixture: f, sourceID: sourceID}
}

func (h usageHarness) record(t *testing.T, at time.Time, events, errs, dropped int64) {
	t.Helper()

	if err := h.store.Record(context.Background(), usagebus.Usage{
		SourceID:     h.sourceID,
		OrgID:        h.fixture.OrgID,
		ProjectID:    h.fixture.ProjectID,
		Day:          at,
		EventCount:   events,
		ByteCount:    events * 100,
		DroppedCount: dropped,
		ErrorCount:   errs,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
}

// Every batch folds into both tallies. Quota is read from the daily row and
// health from the hourly ones, so a source cannot look busy on one and idle on
// the other.
func Test_Usage_Integration_RecordFoldsIntoDailyAndHourly(t *testing.T) {
	h := newUsageHarness(t)

	hour := time.Now().UTC().Truncate(time.Hour)

	// Two batches in the same hour, one in the hour before.
	h.record(t, hour.Add(10*time.Minute), 100, 3, 1)
	h.record(t, hour.Add(20*time.Minute), 50, 2, 0)
	h.record(t, hour.Add(-30*time.Minute), 10, 0, 0)

	var daily struct {
		Events  int64 `db:"event_count"`
		Bytes   int64 `db:"byte_count"`
		Dropped int64 `db:"dropped_count"`
	}
	if err := h.db.DB.Get(&daily, `
		SELECT event_count, byte_count, dropped_count FROM ingest_usage
		WHERE source_id = $1 AND day = $2`, h.sourceID, hour.Truncate(24*time.Hour)); err != nil {
		t.Fatalf("read daily: %v", err)
	}

	// The third batch may land in the previous day near midnight; assert only
	// what is certain about the same-day pair.
	if daily.Events < 150 {
		t.Errorf("daily events = %d, want at least 150", daily.Events)
	}

	var rows []struct {
		Hour    time.Time `db:"hour"`
		Events  int64     `db:"event_count"`
		Errors  int64     `db:"error_count"`
		Dropped int64     `db:"dropped_count"`
	}
	if err := h.db.DB.Select(&rows, `
		SELECT hour, event_count, error_count, dropped_count FROM ingest_stats_hourly
		WHERE source_id = $1 ORDER BY hour`, h.sourceID); err != nil {
		t.Fatalf("read hourly: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d hourly rows, want 2", len(rows))
	}
	if rows[0].Events != 10 {
		t.Errorf("earlier hour events = %d, want 10", rows[0].Events)
	}
	if rows[1].Events != 150 || rows[1].Errors != 5 || rows[1].Dropped != 1 {
		t.Errorf("current hour = %d/%d/%d, want 150/5/1", rows[1].Events, rows[1].Errors, rows[1].Dropped)
	}
	if !rows[1].Hour.UTC().Equal(hour) {
		t.Errorf("current hour bucket = %v, want %v", rows[1].Hour.UTC(), hour)
	}
}

// The counters query is what the sources list calls once per page. It must total
// only the requested window and only the requested sources.
func Test_Usage_Integration_QuerySourceCounters(t *testing.T) {
	h := newUsageHarness(t)
	ctx := context.Background()

	other := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO sources (id, org_id, project_id, kind, name, key_prefix, key_hash)
		VALUES ($1, $2, $3, 'vector', 'other', 'ls_src_live_def', $4)`,
		other, h.fixture.OrgID, h.fixture.ProjectID, other.String()); err != nil {
		t.Fatalf("insert other source: %v", err)
	}

	hour := time.Now().UTC().Truncate(time.Hour)

	h.record(t, hour, 100, 10, 2)
	h.record(t, hour.Add(-5*time.Hour), 40, 0, 0)
	h.record(t, hour.Add(-40*time.Hour), 999, 999, 0) // outside a 24h window

	if err := h.store.Record(ctx, usagebus.Usage{
		SourceID: other, OrgID: h.fixture.OrgID, ProjectID: h.fixture.ProjectID,
		Day: hour, EventCount: 7, ErrorCount: 1,
	}); err != nil {
		t.Fatalf("record other: %v", err)
	}

	from := hour.Add(time.Hour).Add(-24 * time.Hour)

	counters, err := h.store.QuerySourceCounters(ctx, []uuid.UUID{h.sourceID}, from)
	if err != nil {
		t.Fatalf("query counters: %v", err)
	}

	got, ok := counters[h.sourceID]
	if !ok {
		t.Fatalf("no counters returned for the source")
	}
	if got.Events != 140 || got.Errors != 10 || got.Dropped != 2 {
		t.Errorf("counters = %d/%d/%d, want 140/10/2 (the 40 hour old batch excluded)", got.Events, got.Errors, got.Dropped)
	}
	if rate := got.ErrorRate(); rate < 0.07 || rate > 0.08 {
		t.Errorf("error rate = %v, want ~0.071", rate)
	}
	if _, ok := counters[other]; ok {
		t.Errorf("counters include a source that was not asked for")
	}

	// Both sources at once, which is how the list calls it.
	both, err := h.store.QuerySourceCounters(ctx, []uuid.UUID{h.sourceID, other}, from)
	if err != nil {
		t.Fatalf("query both: %v", err)
	}
	if len(both) != 2 {
		t.Errorf("got %d sources, want 2", len(both))
	}
	if both[other].Events != 7 {
		t.Errorf("other source events = %d, want 7", both[other].Events)
	}

	// A source with no ingest in the window is absent rather than zero, so the
	// caller can tell "nothing arrived" from "no such source".
	quiet := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO sources (id, org_id, project_id, kind, name, key_prefix, key_hash)
		VALUES ($1, $2, $3, 'k8s', 'quiet', 'ls_src_live_ghi', $4)`,
		quiet, h.fixture.OrgID, h.fixture.ProjectID, quiet.String()); err != nil {
		t.Fatalf("insert quiet source: %v", err)
	}

	withQuiet, err := h.store.QuerySourceCounters(ctx, []uuid.UUID{h.sourceID, quiet}, from)
	if err != nil {
		t.Fatalf("query with quiet: %v", err)
	}
	if _, ok := withQuiet[quiet]; ok {
		t.Errorf("a source with no ingest was returned with counters")
	}
}

// The detail endpoint plots these buckets, so they must come back ordered,
// bounded by the window, and stamped on the hour in UTC.
func Test_Usage_Integration_QuerySourceBuckets(t *testing.T) {
	h := newUsageHarness(t)
	ctx := context.Background()

	end := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	start := end.Add(-24 * time.Hour)

	for _, offset := range []int{-1, -3, -25} {
		h.record(t, end.Add(time.Duration(offset)*time.Hour).Add(15*time.Minute), 5, 1, 0)
	}

	buckets, err := h.store.QuerySourceBuckets(ctx, h.sourceID, start, end)
	if err != nil {
		t.Fatalf("query buckets: %v", err)
	}

	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2: the 25 hour old row is outside the window", len(buckets))
	}
	if !buckets[0].Hour.Before(buckets[1].Hour) {
		t.Errorf("buckets are not ordered oldest first: %v then %v", buckets[0].Hour, buckets[1].Hour)
	}
	for i, b := range buckets {
		if !b.Hour.Equal(b.Hour.Truncate(time.Hour)) {
			t.Errorf("buckets[%d].Hour = %v, want an exact hour", i, b.Hour)
		}
		if b.Events != 5 || b.Errors != 1 {
			t.Errorf("buckets[%d] = %d/%d, want 5/1", i, b.Events, b.Errors)
		}
	}
}

// Health counters are derived data with no consistency contract against logs, so
// retention just ages them out — and must leave the window it reads intact.
func Test_Usage_Integration_RetentionPrunesOldCounters(t *testing.T) {
	h := newUsageHarness(t)

	hour := time.Now().UTC().Truncate(time.Hour)

	h.record(t, hour, 5, 0, 0)
	h.record(t, hour.Add(-20*24*time.Hour), 5, 0, 0)
	h.record(t, hour.Add(-60*24*time.Hour), 5, 0, 0)

	res, err := retention.Run(context.Background(), h.db.Log, h.db.DB, retention.Config{SourceStatsDays: 14})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.SourceStatsDeleted != 2 {
		t.Errorf("SourceStatsDeleted = %d, want 2", res.SourceStatsDeleted)
	}
	if res.Total() != 0 {
		t.Errorf("Total() = %d, want 0: pruned counters must not consume the row budget", res.Total())
	}

	var remaining int64
	if err := h.db.DB.Get(&remaining, `SELECT count(1) FROM ingest_stats_hourly WHERE source_id = $1`, h.sourceID); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Errorf("%d counter rows remain, want 1", remaining)
	}

	// Negative keeps them forever, which is the escape hatch for a longer window.
	h.record(t, hour.Add(-90*24*time.Hour), 5, 0, 0)
	res, err = retention.Run(context.Background(), h.db.Log, h.db.DB, retention.Config{SourceStatsDays: -1})
	if err != nil {
		t.Fatalf("run with keep-forever: %v", err)
	}
	if res.SourceStatsDeleted != 0 {
		t.Errorf("SourceStatsDeleted = %d, want 0 with a negative retention", res.SourceStatsDeleted)
	}
}
