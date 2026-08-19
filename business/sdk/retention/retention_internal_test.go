package retention

import (
	"testing"
	"time"
)

func Test_expires(t *testing.T) {
	cases := []struct {
		name string
		days int
		want bool
	}{
		{"keep forever sentinel", -1, false},
		{"anything below the sentinel also keeps", -5, false},
		{"zero retains nothing, so it expires", 0, true},
		{"a normal window expires", 7, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := expires(c.days); got != c.want {
				t.Errorf("expires(%d) = %v, want %v", c.days, got, c.want)
			}
		})
	}
}

func Test_boundaryHour(t *testing.T) {
	// A cutoff mid-hour must yield that whole UTC hour, so the partially deleted
	// hour gets recomputed rather than dropped or left stale.
	cutoff := time.Date(2026, 8, 12, 15, 47, 3, 500, time.UTC)

	start, end := boundaryHour(cutoff)

	wantStart := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", start, wantStart)
	}
	if !end.Equal(wantStart.Add(time.Hour)) {
		t.Errorf("end = %v, want start+1h", end)
	}
	if !cutoff.After(start) || !cutoff.Before(end) {
		t.Error("cutoff must fall inside its own boundary window")
	}
}

// A cutoff in a zone whose offset is not a whole hour must still land on a UTC
// hour boundary, or the repaired rows would be filed under a bucket the ingest
// path never writes.
func Test_boundaryHour_NormalisesToUTC(t *testing.T) {
	kathmandu := time.FixedZone("kathmandu", 5*60*60+45*60)

	// 15:30 +05:45 is 09:45 UTC, so the bucket is 09:00 UTC.
	cutoff := time.Date(2026, 8, 12, 15, 30, 0, 0, kathmandu)

	start, _ := boundaryHour(cutoff)

	want := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	if !start.Equal(want) {
		t.Errorf("start = %v, want %v", start, want)
	}
	if start.Location() != time.UTC {
		t.Errorf("start location = %v, want UTC", start.Location())
	}
	if start.Minute() != 0 {
		t.Errorf("start minute = %d, want a whole UTC hour", start.Minute())
	}
}

func Test_Config_withDefaults(t *testing.T) {
	// A zero BatchSize would make the delete loop issue LIMIT 0 forever.
	if got := (Config{}).withDefaults().BatchSize; got <= 0 {
		t.Errorf("BatchSize = %d, want a positive default", got)
	}
	if got := (Config{BatchSize: 500}).withDefaults().BatchSize; got != 500 {
		t.Errorf("BatchSize = %d, want the caller's 500", got)
	}

	d := DefaultConfig()
	if d.BatchSize <= 0 || d.MaxRows <= 0 || d.MaxRuntime <= 0 {
		t.Errorf("DefaultConfig must be bounded on all three axes: %+v", d)
	}
}

func Test_Result_Total(t *testing.T) {
	r := Result{InfraDeleted: 3, AppDeleted: 4}
	if r.Total() != 7 {
		t.Errorf("Total() = %d, want 7", r.Total())
	}
}

func Test_nextBatch(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		spent     int64
		deleted   int64
		wantBatch int
		wantOK    bool
	}{
		{
			name:      "no row budget uses the full batch",
			cfg:       Config{BatchSize: 10_000},
			wantBatch: 10_000,
			wantOK:    true,
		},
		{
			name:      "budget with room to spare uses the full batch",
			cfg:       Config{BatchSize: 10_000, MaxRows: 100_000},
			spent:     50_000,
			wantBatch: 10_000,
			wantOK:    true,
		},
		{
			// The last batch is trimmed so a run cannot overshoot MaxRows.
			name:      "budget nearly spent trims the batch",
			cfg:       Config{BatchSize: 10_000, MaxRows: 100_000},
			spent:     95_000,
			wantBatch: 5_000,
			wantOK:    true,
		},
		{
			name:      "spend counted across projects and within this one",
			cfg:       Config{BatchSize: 10_000, MaxRows: 100_000},
			spent:     90_000,
			deleted:   7_000,
			wantBatch: 3_000,
			wantOK:    true,
		},
		{
			// This is the stop signal. The caller must still repair the rollup for
			// rows already deleted; skipping it left stats over-reporting.
			name:   "budget exactly spent stops",
			cfg:    Config{BatchSize: 10_000, MaxRows: 100_000},
			spent:  100_000,
			wantOK: false,
		},
		{
			name:    "budget overshot stops",
			cfg:     Config{BatchSize: 10_000, MaxRows: 100_000},
			spent:   99_000,
			deleted: 5_000,
			wantOK:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			batch, ok := nextBatch(c.cfg, c.spent, c.deleted)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && batch != c.wantBatch {
				t.Errorf("batch = %d, want %d", batch, c.wantBatch)
			}
			if ok && batch <= 0 {
				t.Error("a usable batch must be positive or the loop cannot progress")
			}
		})
	}
}
