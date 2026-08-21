package rejectdb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/domain/rejectbus/stores/rejectdb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
	"github.com/jkarage/logingestor/business/sdk/ingest"
	"github.com/jkarage/logingestor/business/sdk/retention"
)

type harness struct {
	db       *dbtest.Database
	bus      *rejectbus.Business
	fixture  dbtest.Fixture
	sourceID uuid.UUID
}

func newHarness(t *testing.T, cap int) harness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	sourceID := uuid.New()
	if _, err := db.DB.Exec(`
		INSERT INTO sources (id, org_id, project_id, kind, name, key_prefix, key_hash)
		VALUES ($1, $2, $3, 'fluentbit', 'shipper', 'ls_src_live_abc', $4)`,
		sourceID, f.OrgID, f.ProjectID, sourceID.String()); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	// The real redactor, because scrubbing a payload the normaliser never saw is
	// the whole reason this store takes one.
	bus := rejectbus.NewBusiness(db.Log, rejectdb.NewStore(db.Log, db.DB), ingest.NewRedactor(), cap)

	return harness{db: db, bus: bus, fixture: f, sourceID: sourceID}
}

func (h harness) reject(kind, reason, payload string, index int) rejectbus.NewReject {
	return rejectbus.NewReject{
		SourceID:    h.sourceID,
		OrgID:       h.fixture.OrgID,
		ProjectID:   h.fixture.ProjectID,
		Kind:        kind,
		RecordIndex: index,
		Reason:      reason,
		Payload:     payload,
	}
}

// The point of the store: the record itself, which is the only thing that
// explains a refusal once nobody is reading the ingest responses.
func Test_Rejects_Integration_StoreAndRead(t *testing.T) {
	h := newHarness(t, 100)
	ctx := context.Background()

	stored, err := h.bus.Store(ctx, []rejectbus.NewReject{
		h.reject(rejectbus.KindParse, "invalid JSON: unexpected end of input", `{"message":"half a lin`, 3),
		h.reject(rejectbus.KindValidate, "message is required", `{"level":"INFO","source":"api"}`, 7),
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if stored != 2 {
		t.Fatalf("stored %d, want 2", stored)
	}

	out, err := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("read %d rejects, want 2", len(out))
	}

	// Newest first, and the payload and index survive — an index without the
	// record is what the response already gave the sender.
	var byKind = map[string]rejectbus.Reject{}
	for _, r := range out {
		byKind[r.Kind] = r
	}

	if got := byKind[rejectbus.KindParse]; got.RecordIndex != 3 || !strings.Contains(got.Payload, "half a lin") {
		t.Errorf("parse reject = %+v", got)
	}
	if got := byKind[rejectbus.KindValidate]; got.Reason != "message is required" {
		t.Errorf("validate reject = %+v", got)
	}

	counts, err := h.bus.CountByKind(ctx, h.fixture.OrgID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[rejectbus.KindParse] != 1 || counts[rejectbus.KindValidate] != 1 {
		t.Errorf("counts = %v, want one of each", counts)
	}

	// Filters narrow.
	if got, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID, Kind: rejectbus.KindParse}); len(got) != 1 {
		t.Errorf("kind filter returned %d", len(got))
	}
	if got, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID, SourceID: &h.sourceID}); len(got) != 2 {
		t.Errorf("source filter returned %d", len(got))
	}

	other := uuid.New()
	if got, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID, SourceID: &other}); len(got) != 0 {
		t.Errorf("another source's filter returned rows")
	}
	if got, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: uuid.New()}); len(got) != 0 {
		t.Errorf("another org read this org's rejects")
	}
}

// A refused record never reaches the normaliser that would have redacted it, so
// the scrubbing has to happen here — and a rejected record is exactly the kind
// that has a token pasted where a message should be.
func Test_Rejects_Integration_PayloadIsScrubbed(t *testing.T) {
	h := newHarness(t, 100)
	ctx := context.Background()

	dirty := `{"message":"login failed for joseph@bsa.ai","headers":{"authorization":"Bearer abc123def456ghi"},"apiKey":"sk_live_9f8e7d6c5b4a"}`

	if _, err := h.bus.Store(ctx, []rejectbus.NewReject{
		h.reject(rejectbus.KindValidate, "level is required", dirty, 0),
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	var payload string
	if err := h.db.DB.Get(&payload, `SELECT payload FROM ingest_rejects LIMIT 1`); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	for _, secret := range []string{"joseph@bsa.ai", "abc123def456ghi", "sk_live_9f8e7d6c5b4a"} {
		if strings.Contains(payload, secret) {
			t.Errorf("secret %q survived into the dead-letter store: %s", secret, payload)
		}
	}
	// Still readable enough to diagnose.
	if !strings.Contains(payload, "login failed") {
		t.Errorf("scrubbing destroyed the diagnostic value: %s", payload)
	}
}

// A broken shipper refuses everything at full volume. Storing all of it would
// mean the flood lands in the database instead of being shed.
func Test_Rejects_Integration_HourlyCap(t *testing.T) {
	h := newHarness(t, 5)
	ctx := context.Background()

	batch := make([]rejectbus.NewReject, 0, 20)
	for i := 0; i < 20; i++ {
		batch = append(batch, h.reject(rejectbus.KindParse, "invalid JSON", `{"broken`, i))
	}

	stored, err := h.bus.Store(ctx, batch)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if stored != 5 {
		t.Errorf("stored %d of 20 with a cap of 5", stored)
	}

	// And the next request stores nothing at all, rather than resetting.
	stored, err = h.bus.Store(ctx, batch)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if stored != 0 {
		t.Errorf("stored %d more after the cap was reached", stored)
	}

	out, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID})
	if len(out) != 5 {
		t.Errorf("%d rows in the store, want the cap of 5", len(out))
	}

	// The cap is per hour, so an older hour does not count against this one.
	if _, err := h.db.DB.Exec(`
		UPDATE ingest_rejects SET received_at = NOW() - interval '2 hours'`); err != nil {
		t.Fatalf("age rows: %v", err)
	}
	stored, err = h.bus.Store(ctx, batch)
	if err != nil {
		t.Fatalf("store after the hour rolled: %v", err)
	}
	if stored != 5 {
		t.Errorf("stored %d after the hour rolled, want a fresh cap of 5", stored)
	}
}

// The store is a sample; the exact count lives with the other ingest counters,
// so the two together stay honest even when the cap bites.
func Test_Rejects_Integration_ExactCountIsElsewhere(t *testing.T) {
	h := newHarness(t, 2)
	ctx := context.Background()

	if _, err := h.db.DB.Exec(`
		INSERT INTO ingest_stats_hourly (source_id, hour, event_count, reject_count)
		VALUES ($1, date_trunc('hour', now()), 0, 20)`, h.sourceID); err != nil {
		t.Fatalf("record the exact count: %v", err)
	}

	batch := make([]rejectbus.NewReject, 0, 20)
	for i := 0; i < 20; i++ {
		batch = append(batch, h.reject(rejectbus.KindParse, "invalid JSON", `{"broken`, i))
	}
	if _, err := h.bus.Store(ctx, batch); err != nil {
		t.Fatalf("store: %v", err)
	}

	var exact int64
	if err := h.db.DB.Get(&exact, `SELECT reject_count FROM ingest_stats_hourly WHERE source_id = $1`, h.sourceID); err != nil {
		t.Fatalf("read the exact count: %v", err)
	}
	if exact != 20 {
		t.Errorf("exact count = %d, want all 20", exact)
	}

	kept, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID})
	if len(kept) != 2 {
		t.Errorf("kept %d samples, want the cap of 2", len(kept))
	}
}

// Deleting a source takes its refused records with it, and retention ages the
// rest out — this is a debugging aid for a live problem, not a record.
func Test_Rejects_Integration_RetentionAndCascade(t *testing.T) {
	h := newHarness(t, 100)
	ctx := context.Background()

	if _, err := h.bus.Store(ctx, []rejectbus.NewReject{
		h.reject(rejectbus.KindParse, "old", `{"a":1}`, 0),
		h.reject(rejectbus.KindParse, "recent", `{"b":2}`, 1),
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	if _, err := h.db.DB.Exec(`
		UPDATE ingest_rejects SET received_at = NOW() - interval '30 days' WHERE reason = 'old'`); err != nil {
		t.Fatalf("age: %v", err)
	}

	res, err := retention.Run(ctx, h.db.Log, h.db.DB, retention.Config{RejectDays: 7})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.RejectsDeleted != 1 {
		t.Errorf("deleted %d, want 1", res.RejectsDeleted)
	}
	if res.Total() != 0 {
		t.Errorf("Total() = %d: diagnostics must not consume the log budget", res.Total())
	}

	left, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID})
	if len(left) != 1 || left[0].Reason != "recent" {
		t.Errorf("%d rejects remain, want the recent one", len(left))
	}

	// Negative keeps them, which is the escape hatch for a long investigation.
	if _, err := h.db.DB.Exec(`UPDATE ingest_rejects SET received_at = NOW() - interval '400 days'`); err != nil {
		t.Fatalf("age everything: %v", err)
	}
	res, err = retention.Run(ctx, h.db.Log, h.db.DB, retention.Config{RejectDays: -1})
	if err != nil {
		t.Fatalf("keep-forever run: %v", err)
	}
	if res.RejectsDeleted != 0 {
		t.Errorf("keep-forever deleted %d", res.RejectsDeleted)
	}

	// The source going away takes them too.
	if _, err := h.db.DB.Exec(`DELETE FROM sources WHERE id = $1`, h.sourceID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if got, _ := h.bus.Query(ctx, rejectbus.Filter{OrgID: h.fixture.OrgID}); len(got) != 0 {
		t.Errorf("%d rejects survived their source", len(got))
	}
}

// Every field a sender controls has to be bounded, and truncation must not split
// a rune or Postgres rejects the whole insert.
func Test_Rejects_Integration_BoundsPayloads(t *testing.T) {
	h := newHarness(t, 100)
	ctx := context.Background()

	if _, err := h.bus.Store(ctx, []rejectbus.NewReject{
		h.reject(rejectbus.KindParse, strings.Repeat("r", 5_000), strings.Repeat("日", 20_000), 0),
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	var row struct {
		Reason  string `db:"reason"`
		Payload string `db:"payload"`
	}
	if err := h.db.DB.Get(&row, `SELECT reason, payload FROM ingest_rejects LIMIT 1`); err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(row.Reason) > rejectbus.MaxReasonLen+16 {
		t.Errorf("reason is %d bytes", len(row.Reason))
	}
	if len(row.Payload) > rejectbus.MaxPayloadBytes+16 {
		t.Errorf("payload is %d bytes", len(row.Payload))
	}
	if !strings.HasSuffix(row.Payload, "[truncated]") {
		t.Errorf("a truncated payload does not say so")
	}
}
