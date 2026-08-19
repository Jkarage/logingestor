package logdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
)

// Test_toDBLog_InfraRoundTrip verifies infra fields and source metadata survive
// the bus<->db conversion, and that empty infra strings become SQL NULLs.
func Test_toDBLog_InfraRoundTrip(t *testing.T) {
	srcID := uuid.New()
	in := logbus.Log{
		ID:         uuid.New(),
		ProjectID:  uuid.New(),
		Level:      logbus.LevelWarn,
		Message:    "disk pressure",
		Source:     "kubelet",
		Timestamp:  time.Now().UTC().Truncate(time.Second),
		Tags:       []string{"k8s"},
		Meta:       map[string]any{"trace_id": "abc"},
		SourceType: logbus.SourceTypeInfra,
		SourceID:   &srcID,
		Infra: logbus.Infra{
			Host:      "node-1",
			Pod:       "api-7c9",
			Namespace: "prod",
			// Container intentionally empty -> expect NULL.
		},
		Attributes: map[string]any{"_severity": 4},
	}

	db := toDBLog(in)

	if db.SourceType != logbus.SourceTypeInfra {
		t.Errorf("source_type = %q, want infra", db.SourceType)
	}
	if db.SourceID == nil || *db.SourceID != srcID {
		t.Errorf("source_id not preserved: %v", db.SourceID)
	}
	if !db.Host.Valid || db.Host.String != "node-1" {
		t.Errorf("host = %+v, want valid node-1", db.Host)
	}
	if db.Container.Valid {
		t.Errorf("empty container should be NULL, got %+v", db.Container)
	}

	out, err := toBusLog(db)
	if err != nil {
		t.Fatalf("toBusLog: %v", err)
	}
	if out.SourceType != logbus.SourceTypeInfra {
		t.Errorf("round-trip source_type = %q", out.SourceType)
	}
	if out.Infra.Pod != "api-7c9" || out.Infra.Namespace != "prod" {
		t.Errorf("round-trip infra mismatch: %+v", out.Infra)
	}
	if out.Infra.Container != "" {
		t.Errorf("round-trip container = %q, want empty", out.Infra.Container)
	}
}

// Test_toDBLog_AppDefaults verifies an entry with no source_type defaults to
// "app" and nil attributes become an empty map (back-compat with app logs).
func Test_toDBLog_AppDefaults(t *testing.T) {
	db := toDBLog(logbus.Log{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
		Level:     logbus.LevelInfo,
		Message:   "hello",
		Source:    "web",
		Timestamp: time.Now(),
	})

	if db.SourceType != logbus.SourceTypeApp {
		t.Errorf("source_type = %q, want app", db.SourceType)
	}
	if db.Attributes == nil {
		t.Error("attributes should default to non-nil empty map")
	}
	if db.SourceID != nil {
		t.Errorf("source_id should be nil for app logs, got %v", db.SourceID)
	}
}

// Test_insertBatchSize_StaysUnderParamLimit guards the chunk size against
// Postgres' 65535 bound-parameter ceiling given the logs column count.
func Test_insertBatchSize_StaysUnderParamLimit(t *testing.T) {
	const columns = 20 // keep in sync with the INSERT column list
	if insertBatchSize*columns >= 65535 {
		t.Fatalf("insertBatchSize %d * %d cols = %d exceeds pg param limit",
			insertBatchSize, columns, insertBatchSize*columns)
	}
}

// Test_rollupDeltas_CollapsesBatch verifies a batch becomes one increment per
// (project, UTC hour, source_type, source, level) rather than one row per log,
// and that the hour is bucketed in UTC.
func Test_rollupDeltas_CollapsesBatch(t *testing.T) {
	proj := uuid.New()
	base := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	logs := []logbus.Log{
		{ProjectID: proj, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeApp, Source: "api", Timestamp: base},
		{ProjectID: proj, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeApp, Source: "api", Timestamp: base.Add(30 * time.Minute)},
		{ProjectID: proj, Level: logbus.LevelWarn, SourceType: logbus.SourceTypeApp, Source: "api", Timestamp: base},
		{ProjectID: proj, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeApp, Source: "worker", Timestamp: base},
		{ProjectID: proj, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeInfra, Source: "api", Timestamp: base},
		// Next hour: must not fold into the bucket above.
		{ProjectID: proj, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeApp, Source: "api", Timestamp: base.Add(time.Hour)},
	}

	got := rollupDeltas(logs)

	// app/api/INFO@12 (2), app/api/WARN@12, app/worker/INFO@12,
	// infra/api/INFO@12, app/api/INFO@13 => 5 distinct buckets.
	if len(got) != 5 {
		t.Fatalf("got %d deltas, want 5: %+v", len(got), got)
	}

	total := 0
	for _, d := range got {
		total += d.Count
		if d.Hour.Minute() != 0 || d.Hour.Second() != 0 {
			t.Errorf("hour %v is not hour-aligned", d.Hour)
		}
		if d.Hour.Location() != time.UTC {
			t.Errorf("hour %v not in UTC", d.Hour)
		}
	}
	if total != len(logs) {
		t.Errorf("delta counts sum to %d, want %d", total, len(logs))
	}
}

// A +05:45 offset must still bucket onto a UTC hour boundary; truncating in the
// local zone would land at :45 and split one hour across two rollup rows.
func Test_UTCHour_NonWholeHourOffset(t *testing.T) {
	kathmandu := time.FixedZone("kathmandu", 5*60*60+45*60)
	got := UTCHour(time.Date(2026, 8, 19, 15, 30, 0, 0, kathmandu))

	want := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("UTCHour = %v, want %v", got, want)
	}
	if got.Minute() != 0 {
		t.Errorf("minute = %d, want 0", got.Minute())
	}
}

// Test_Query_TotalIsBounded guards the fix for the ~10s login stall: an exact
// count(1) over a project holding most of the logs table degenerates into a
// sequential scan. The default must stay capped.
func Test_Query_TotalIsBounded(t *testing.T) {
	if logbus.TotalBounded != 0 {
		t.Fatal("TotalBounded must be the zero value so an unset filter is capped, not exact")
	}
	if logbus.TotalCap <= 0 {
		t.Fatalf("TotalCap = %d, want a positive cap", logbus.TotalCap)
	}
}
