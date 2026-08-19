package logbus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

func Test_ParseInterval(t *testing.T) {
	for _, name := range []string{"1m", "5m", "15m", "1h", "6h", "1d"} {
		got, err := ParseInterval(name)
		if err != nil {
			t.Errorf("ParseInterval(%q): %v", name, err)
			continue
		}
		if got.String() != name {
			t.Errorf("String() = %q, want %q", got.String(), name)
		}
	}

	for _, bad := range []string{"", "2m", "1s", "1w", "60", "1h ", "DROP"} {
		if _, err := ParseInterval(bad); !errors.Is(err, ErrInvalidInterval) {
			t.Errorf("ParseInterval(%q) err = %v, want ErrInvalidInterval", bad, err)
		}
	}
}

func Test_ParseGroupBy(t *testing.T) {
	for _, col := range []string{"level", "source", "source_type"} {
		g, err := ParseGroupBy(col)
		if err != nil {
			t.Fatalf("ParseGroupBy(%q): %v", col, err)
		}
		if c, ok := g.Column(); !ok || c != col {
			t.Errorf("Column() = %q,%v want %q,true", c, ok, col)
		}
		if !g.fromRollup() {
			t.Errorf("%q should be served from the rollup", col)
		}
	}

	g, err := ParseGroupBy("meta.orderId")
	if err != nil {
		t.Fatalf("meta.orderId: %v", err)
	}
	if g.MetaField() != "orderId" {
		t.Errorf("MetaField() = %q, want orderId", g.MetaField())
	}
	if _, ok := g.Column(); ok {
		t.Error("a meta group-by must not report a column")
	}
	if g.fromRollup() {
		t.Error("meta group-by has no rollup and must scan logs")
	}
	if g.String() != "meta.orderId" {
		t.Errorf("String() = %q", g.String())
	}

	// A group-by column is interpolated into SQL, so anything outside the closed
	// set — and any meta field with SQL punctuation — must be rejected.
	for _, bad := range []string{
		"", "ts", "message", "meta.", "meta", "count(*)",
		"level; DROP TABLE logs", `meta.a"b`, "meta.a'b", "meta.a b", "meta.a)b",
	} {
		if _, err := ParseGroupBy(bad); !errors.Is(err, ErrInvalidGroupBy) {
			t.Errorf("ParseGroupBy(%q) err = %v, want ErrInvalidGroupBy", bad, err)
		}
	}
}

func Test_TimeseriesRequest_validate(t *testing.T) {
	base := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)

	t.Run("defaults fill a 24h window", func(t *testing.T) {
		r := TimeseriesRequest{Interval: Interval1h}
		if err := r.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got := r.To.Sub(r.From); got != DefaultWindow {
			t.Errorf("window = %v, want %v", got, DefaultWindow)
		}
	})

	t.Run("inverted range rejected", func(t *testing.T) {
		r := TimeseriesRequest{From: base, To: base.Add(-time.Hour), Interval: Interval1h}
		if err := r.validate(); !errors.Is(err, ErrInvalidRange) {
			t.Errorf("err = %v, want ErrInvalidRange", err)
		}
	})

	// 1m buckets across a year would be millions of points.
	t.Run("bucket explosion rejected", func(t *testing.T) {
		r := TimeseriesRequest{From: base, To: base.AddDate(1, 0, 0), Interval: Interval1m}
		if err := r.validate(); !errors.Is(err, ErrTooManyBuckets) {
			t.Errorf("err = %v, want ErrTooManyBuckets", err)
		}
	})

	// Sub-hour intervals scan logs, so the window is capped even when the bucket
	// count is fine.
	t.Run("sub-hour window capped", func(t *testing.T) {
		r := TimeseriesRequest{From: base, To: base.Add(MaxRawWindow + time.Minute), Interval: Interval15m}
		if err := r.validate(); !errors.Is(err, ErrWindowTooWide) {
			t.Errorf("err = %v, want ErrWindowTooWide", err)
		}
	})

	// The same width is fine at 1h, which reads the rollup.
	t.Run("hourly interval not window-capped", func(t *testing.T) {
		r := TimeseriesRequest{From: base, To: base.AddDate(0, 0, 30), Interval: Interval1d}
		if err := r.validate(); err != nil {
			t.Errorf("validate: %v", err)
		}
	})
}

func Test_AggregateRequest_validate(t *testing.T) {
	base := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	level, _ := ParseGroupBy("level")
	meta, _ := ParseGroupBy("meta.orderId")

	t.Run("limit defaulted and capped", func(t *testing.T) {
		r := AggregateRequest{GroupBy: level}
		if err := r.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if r.Limit <= 0 {
			t.Errorf("Limit = %d, want a positive default", r.Limit)
		}

		r2 := AggregateRequest{GroupBy: level, Limit: MaxAggregateLimit * 10}
		if err := r2.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if r2.Limit != MaxAggregateLimit {
			t.Errorf("Limit = %d, want %d", r2.Limit, MaxAggregateLimit)
		}
	})

	t.Run("rollup group-by allows a wide window", func(t *testing.T) {
		r := AggregateRequest{From: base, To: base.AddDate(0, 0, 90), GroupBy: level}
		if err := r.validate(); err != nil {
			t.Errorf("validate: %v", err)
		}
	})

	t.Run("meta group-by window capped", func(t *testing.T) {
		r := AggregateRequest{From: base, To: base.Add(MaxRawWindow + time.Second), GroupBy: meta}
		if err := r.validate(); !errors.Is(err, ErrWindowTooWide) {
			t.Errorf("err = %v, want ErrWindowTooWide", err)
		}
	})
}

// fakeStorer returns fixed timeseries rows; only Timeseries is exercised.
type fakeStorer struct {
	Storer
	rows []BucketCount
}

func (f fakeStorer) Timeseries(context.Context, TimeseriesRequest) ([]BucketCount, error) {
	return f.rows, nil
}

func Test_Business_Timeseries_FillsGapsAndLevels(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour)

	// Only the middle bucket has data, and only for one level.
	rows := []BucketCount{{TS: from.Add(time.Hour), Level: "ERROR", Count: 7}}

	b := &Business{log: logger.New(nil, logger.LevelInfo, "test", func(context.Context) string { return "" }), storer: fakeStorer{rows: rows}}

	got, err := b.Timeseries(context.Background(), TimeseriesRequest{
		ProjectID: uuid.New(), From: from, To: to, Interval: Interval1h,
	})
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d buckets, want 3 (empty ones must still be present)", len(got))
	}

	for i, bkt := range got {
		wantTS := from.Add(time.Duration(i) * time.Hour)
		if !bkt.TS.Equal(wantTS) {
			t.Errorf("bucket %d ts = %v, want %v", i, bkt.TS, wantTS)
		}
		// Every level key present in every bucket, so clients need no gap-filling.
		if len(bkt.Counts) != len(LevelNames()) {
			t.Errorf("bucket %d has %d levels, want %d", i, len(bkt.Counts), len(LevelNames()))
		}
	}

	if got[1].Counts["ERROR"] != 7 {
		t.Errorf("middle bucket ERROR = %d, want 7", got[1].Counts["ERROR"])
	}
	if got[0].Counts["ERROR"] != 0 || got[2].Counts["ERROR"] != 0 {
		t.Error("empty buckets must report zero, not carry data forward")
	}
}

// A from that is not on an interval boundary must align down, matching the
// floor(epoch/step)*step bucketing the SQL uses.
func Test_Business_Timeseries_AlignsToBoundary(t *testing.T) {
	from := time.Date(2026, 8, 19, 0, 37, 12, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	b := &Business{log: logger.New(nil, logger.LevelInfo, "test", func(context.Context) string { return "" }), storer: fakeStorer{}}

	got, err := b.Timeseries(context.Background(), TimeseriesRequest{
		ProjectID: uuid.New(), From: from, To: to, Interval: Interval1h,
	})
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("no buckets")
	}
	if got[0].TS.Minute() != 0 || got[0].TS.Second() != 0 {
		t.Errorf("first bucket %v is not aligned to the interval", got[0].TS)
	}
	if !got[0].TS.Equal(time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("first bucket = %v, want 00:00", got[0].TS)
	}
}
