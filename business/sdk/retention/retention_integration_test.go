package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
	"github.com/jkarage/logingestor/business/sdk/retention"
)

// hoursAgo is a whole UTC hour offset, so every generated row lands in a
// predictable rollup bucket.
func hoursAgo(n int) time.Time {
	return time.Now().UTC().Truncate(time.Hour).Add(-time.Duration(n) * time.Hour)
}

// spread returns rows placed one per hour going back from `fromHours` for
// `count` hours, which gives the rollup a distinct bucket per row.
func spread(projectID uuid.UUID, sourceType string, fromHours, count int) []dbtest.LogRow {
	rows := make([]dbtest.LogRow, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, dbtest.LogRow{
			ProjectID:  projectID,
			SourceType: sourceType,
			Level:      "INFO",
			TS:         hoursAgo(fromHours + i),
		})
	}

	return rows
}

func countLogs(t *testing.T, d *dbtest.Database, projectID uuid.UUID, sourceType string) int64 {
	t.Helper()

	var n int64
	if err := d.DB.Get(&n, `SELECT count(1) FROM logs WHERE project_id = $1 AND source_type = $2`, projectID, sourceType); err != nil {
		t.Fatalf("count logs: %v", err)
	}

	return n
}

// Retention is only correct if the rollup ends up agreeing with the rows that
// survived. Both production bugs were divergences in opposite directions, and
// neither was visible without real rows to delete.
func Test_Retention_Integration_FullDrain(t *testing.T) {
	db := dbtest.New(t)
	f := db.SeedFixture(t, "free") // free keeps app logs 7 days

	// 30 hours of rows starting 20 days back, plus 5 recent hours.
	old := spread(f.ProjectID, "app", 20*24, 30)
	recent := spread(f.ProjectID, "app", 1, 5)
	db.InsertLogs(t, append(old, recent...))

	res, err := retention.Run(context.Background(), db.Log, db.DB, retention.Config{BatchSize: 7})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.Incomplete {
		t.Errorf("run reported incomplete with no budget set")
	}
	if res.AppDeleted != int64(len(old)) {
		t.Errorf("AppDeleted = %d, want %d", res.AppDeleted, len(old))
	}
	if got := countLogs(t, db, f.ProjectID, "app"); got != int64(len(recent)) {
		t.Errorf("surviving app logs = %d, want %d", got, len(recent))
	}

	db.AssertRollupConverged(t)
}

// A run that stops on its row budget leaves expired rows behind. The rollup has
// to describe exactly what survived: too high was the first bug (repair skipped
// on the budget path), too low was the second (repair keyed off the cutoff, so
// it dropped hours whose rows had not drained yet).
func Test_Retention_Integration_BudgetStopKeepsRollupExact(t *testing.T) {
	db := dbtest.New(t)
	f := db.SeedFixture(t, "free")

	old := spread(f.ProjectID, "app", 20*24, 40)
	recent := spread(f.ProjectID, "app", 1, 3)
	db.InsertLogs(t, append(old, recent...))

	res, err := retention.Run(context.Background(), db.Log, db.DB, retention.Config{BatchSize: 5, MaxRows: 12})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !res.Incomplete {
		t.Errorf("Incomplete = false, want true: 40 expired rows with a 12 row budget")
	}
	if res.AppDeleted != 12 {
		t.Errorf("AppDeleted = %d, want 12 (the budget)", res.AppDeleted)
	}

	wantSurvivors := int64(len(old) + len(recent) - 12)
	if got := countLogs(t, db, f.ProjectID, "app"); got != wantSurvivors {
		t.Errorf("surviving app logs = %d, want %d", got, wantSurvivors)
	}

	// The assertion that matters: the rollup describes the survivors, not the
	// cutoff and not the pre-run state.
	db.AssertRollupConverged(t)

	// Draining the rest must also converge, and must not delete the recent rows.
	for i := 0; i < 20; i++ {
		res, err = retention.Run(context.Background(), db.Log, db.DB, retention.Config{BatchSize: 5, MaxRows: 12})
		if err != nil {
			t.Fatalf("drain run %d: %v", i, err)
		}
		if !res.Incomplete {
			break
		}
	}

	if res.Incomplete {
		t.Fatalf("backlog did not drain in 20 runs")
	}
	if got := countLogs(t, db, f.ProjectID, "app"); got != int64(len(recent)) {
		t.Errorf("after draining, app logs = %d, want %d", got, len(recent))
	}

	db.AssertRollupConverged(t)
}

// A drained source type must lose all of its rollup rows, while the other source
// type in the same project keeps its own untouched.
func Test_Retention_Integration_DrainedSourceTypeDropsItsRollup(t *testing.T) {
	db := dbtest.New(t)
	f := db.SeedFixture(t, "free")

	zero := 0
	db.SetRetentionDays(t, f.ProjectID, &zero) // retain nothing

	db.InsertLogs(t, append(
		spread(f.ProjectID, "app", 1, 6),
		spread(f.ProjectID, "infra", 1, 4)...,
	))

	res, err := retention.Run(context.Background(), db.Log, db.DB, retention.Config{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.AppDeleted != 6 {
		t.Errorf("AppDeleted = %d, want 6", res.AppDeleted)
	}
	if got := countLogs(t, db, f.ProjectID, "app"); got != 0 {
		t.Errorf("app logs = %d, want 0", got)
	}

	// retention_days on the project governs app logs only; infra retention comes
	// from the plan, and the free plan sets no infra_retention_days, so infra
	// keeps forever.
	if res.InfraDeleted != 0 {
		t.Errorf("InfraDeleted = %d, want 0: the free plan has no infra_retention_days", res.InfraDeleted)
	}
	if got := countLogs(t, db, f.ProjectID, "infra"); got != 4 {
		t.Errorf("infra logs = %d, want 4", got)
	}

	var appRollup int64
	if err := db.DB.Get(&appRollup, `
		SELECT COALESCE(sum(count), 0) FROM log_stats_hourly
		WHERE project_id = $1 AND source_type = 'app'`, f.ProjectID); err != nil {
		t.Fatalf("app rollup: %v", err)
	}
	if appRollup != 0 {
		t.Errorf("app rollup = %d, want 0 after a full drain", appRollup)
	}

	db.AssertRollupConverged(t)
}

// The keep-forever sentinel has to survive the whole resolution chain — project
// override, then plan features, then the default — because a stray zero here
// deletes everything.
func Test_Retention_Integration_KeepForever(t *testing.T) {
	db := dbtest.New(t)
	f := db.SeedFixture(t, "enterprise") // log_retention_days: -1

	rows := spread(f.ProjectID, "app", 5*365*24, 10) // five years old
	db.InsertLogs(t, rows)

	res, err := retention.Run(context.Background(), db.Log, db.DB, retention.Config{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.Total() != 0 {
		t.Errorf("deleted %d rows, want 0 on a keep-forever plan", res.Total())
	}
	if got := countLogs(t, db, f.ProjectID, "app"); got != int64(len(rows)) {
		t.Errorf("app logs = %d, want %d", got, len(rows))
	}

	db.AssertRollupConverged(t)
}

// Audit is a compliance surface, so it expires only when configured to. There is
// no rollup to repair here; the risk is the opposite one of deleting by default.
func Test_Retention_Integration_Audit(t *testing.T) {
	db := dbtest.New(t)
	f := db.SeedFixture(t, "free")

	insertAudit := func(age time.Duration) {
		t.Helper()
		if _, err := db.DB.Exec(`
			INSERT INTO audit (id, org_id, obj_id, obj_domain, obj_name, actor_id, action, timestamp)
			VALUES ($1, $2, $3, 'project', 'test', $4, 'update', $5)`,
			uuid.New(), f.OrgID, f.ProjectID, f.UserID, time.Now().UTC().Add(-age)); err != nil {
			t.Fatalf("insert audit: %v", err)
		}
	}

	for i := 0; i < 4; i++ {
		insertAudit(400 * 24 * time.Hour)
	}
	insertAudit(time.Hour)

	countAudit := func() int64 {
		t.Helper()
		var n int64
		if err := db.DB.Get(&n, `SELECT count(1) FROM audit`); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		return n
	}

	// Default config keeps audit forever.
	if _, err := retention.Run(context.Background(), db.Log, db.DB, retention.DefaultConfig()); err != nil {
		t.Fatalf("run with default config: %v", err)
	}
	if got := countAudit(); got != 5 {
		t.Fatalf("audit rows = %d, want 5: the default must not delete audit", got)
	}

	res, err := retention.Run(context.Background(), db.Log, db.DB, retention.Config{AuditDays: 365, BatchSize: 2})
	if err != nil {
		t.Fatalf("run with audit retention: %v", err)
	}
	if res.AuditDeleted != 4 {
		t.Errorf("AuditDeleted = %d, want 4", res.AuditDeleted)
	}
	if got := countAudit(); got != 1 {
		t.Errorf("audit rows = %d, want 1", got)
	}
}
