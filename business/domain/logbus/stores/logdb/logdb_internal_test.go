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
