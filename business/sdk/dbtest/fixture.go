package dbtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Fixture is the minimum row set the log and alerting paths need: an org with a
// plan, an admin member, and a project. Tests add logs and rules on top.
type Fixture struct {
	OrgID        uuid.UUID
	UserID       uuid.UUID
	ProjectID    uuid.UUID
	ConnectionID uuid.UUID

	// PlanSlug is the plan the org is subscribed to, which is what resolves the
	// project's retention when retention_days is null.
	PlanSlug string
}

// SeedFixture inserts an org on the named plan ("free", "pro", "enterprise")
// with one admin, one project and one integration connection.
func (d *Database) SeedFixture(t *testing.T, planSlug string) Fixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	f := Fixture{
		OrgID:     uuid.New(),
		UserID:    uuid.New(),
		ProjectID: uuid.New(),
		PlanSlug:  planSlug,
	}

	suffix := f.OrgID.String()[:8]
	now := time.Now().UTC()

	tx, err := d.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	exec := func(what, query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("fixture %s: %v", what, err)
		}
	}

	exec("user", `
		INSERT INTO users (id, name, email, roles, password_hash, enabled, date_created, date_updated)
		VALUES ($1, $2, $3, ARRAY['USER'], '', true, $4, $4)`,
		f.UserID, "User "+suffix, fmt.Sprintf("user-%s@example.test", suffix), now)

	exec("org", `
		INSERT INTO organizations (id, name, slug, created_at, updated_at, date_created, date_updated, created_by)
		VALUES ($1, $2, $3, $4, $4, $4, $4, $5)`,
		f.OrgID, "Org "+suffix, "org-"+suffix, now, f.UserID)

	exec("membership", `
		INSERT INTO org_members (org_id, user_id, role, joined_at)
		VALUES ($1, $2, 'ORG ADMIN', $3)`,
		f.OrgID, f.UserID, now)

	exec("subscription", `
		INSERT INTO subscriptions (org_id, plan_id, status)
		VALUES ($1, (SELECT id FROM plans WHERE slug = $2), 'active')`,
		f.OrgID, planSlug)

	exec("project", `
		INSERT INTO projects (id, org_id, name, date_created, date_updated)
		VALUES ($1, $2, $3, $4, $4)`,
		f.ProjectID, f.OrgID, "Project "+suffix, now)

	// The provider catalog normally arrives via seed.sql; one row is enough to
	// satisfy the integrations foreign key.
	exec("provider", `
		INSERT INTO integration_providers (id, name, icon, type, description)
		VALUES ('slack', 'Slack', '💬', 'Messaging', 'test')
		ON CONFLICT (id) DO NOTHING`)

	// The credential blob is deliberately junk: this connection exists to satisfy
	// foreign keys. A test that delivers an alert has to create its own
	// connection through the business API so the credentials round-trip.
	if err := tx.GetContext(ctx, &f.ConnectionID, `
		INSERT INTO integrations (org_id, project_id, provider_id, name, credentials_enc, credentials_iv, date_created, date_updated)
		VALUES ($1, $2, 'slack', $3, '\x00', '\x00', $4, $4)
		RETURNING id`,
		f.OrgID, f.ProjectID, "conn-"+suffix, now); err != nil {
		t.Fatalf("fixture connection: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture: %v", err)
	}

	return f
}

// SetRetentionDays overrides the project's own retention, which takes precedence
// over the plan's.
func (d *Database) SetRetentionDays(t *testing.T, projectID uuid.UUID, days *int) {
	t.Helper()

	if _, err := d.DB.Exec(`UPDATE projects SET retention_days = $1 WHERE id = $2`, days, projectID); err != nil {
		t.Fatalf("set retention days: %v", err)
	}
}

// LogRow is one row to insert. Zero values are filled in with usable defaults.
type LogRow struct {
	ProjectID  uuid.UUID
	Level      string
	Message    string
	Source     string
	SourceType string
	TS         time.Time
	Tags       []string
	Meta       string
}

// InsertLogs writes rows straight to the logs table and keeps log_stats_hourly
// in step, the way the ingest path does. Tests that assert on the rollup depend
// on both being written together.
func (d *Database) InsertLogs(t *testing.T, rows []LogRow) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tx, err := d.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin insert logs: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, r := range rows {
		if r.Level == "" {
			r.Level = "INFO"
		}
		if r.Message == "" {
			r.Message = fmt.Sprintf("message %d", i)
		}
		if r.Source == "" {
			r.Source = "api"
		}
		if r.SourceType == "" {
			r.SourceType = "app"
		}
		if r.TS.IsZero() {
			r.TS = time.Now().UTC()
		}
		if r.Meta == "" {
			r.Meta = "{}"
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO logs (project_id, level, message, source, source_type, ts, tags, meta)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			r.ProjectID, r.Level, r.Message, r.Source, r.SourceType, r.TS, pgTextArray(r.Tags), r.Meta); err != nil {
			t.Fatalf("insert log %d: %v", i, err)
		}
	}

	if err := rebuildRollupTx(ctx, tx); err != nil {
		t.Fatalf("rebuild rollup: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit logs: %v", err)
	}
}

// RollupTotals returns the rollup's summed count per project and source type,
// alongside the same figure counted from the logs themselves. Retention keeps
// these two in step; every rollup bug so far has been a divergence between them.
func (d *Database) RollupTotals(t *testing.T) (rollup, actual map[string]int64) {
	t.Helper()

	rollup = map[string]int64{}
	actual = map[string]int64{}

	read := func(query string, into map[string]int64) {
		t.Helper()

		rowsAny, err := d.DB.Query(query)
		if err != nil {
			t.Fatalf("totals query: %v", err)
		}
		defer rowsAny.Close()

		for rowsAny.Next() {
			var key string
			var n int64
			if err := rowsAny.Scan(&key, &n); err != nil {
				t.Fatalf("scan totals: %v", err)
			}
			into[key] = n
		}
		if err := rowsAny.Err(); err != nil {
			t.Fatalf("totals rows: %v", err)
		}
	}

	read(`SELECT project_id::text || ':' || source_type, sum(count) FROM log_stats_hourly GROUP BY 1`, rollup)
	read(`SELECT project_id::text || ':' || source_type, count(1) FROM logs GROUP BY 1`, actual)

	return rollup, actual
}

// AssertRollupConverged fails unless the rollup and the logs agree exactly, in
// both directions. Checking one direction only is how a run once reported
// convergence while the rollup was 41 million rows short.
func (d *Database) AssertRollupConverged(t *testing.T) {
	t.Helper()

	rollup, actual := d.RollupTotals(t)

	for key, want := range actual {
		if got := rollup[key]; got != want {
			t.Errorf("rollup for %s = %d, want %d (logs)", key, got, want)
		}
	}
	for key, got := range rollup {
		if _, ok := actual[key]; !ok && got != 0 {
			t.Errorf("rollup for %s = %d, but no logs survive for it", key, got)
		}
	}
}

// rebuildRollupTx recomputes log_stats_hourly from the logs in the same
// transaction, bucketing in UTC exactly as the ingest and repair paths do.
func rebuildRollupTx(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM log_stats_hourly`); err != nil {
		return err
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO log_stats_hourly (project_id, hour, source_type, source, level, count)
		SELECT
			project_id,
			(date_trunc('hour', ts AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC',
			source_type,
			source,
			level,
			count(*)
		FROM logs
		GROUP BY 1, 2, 3, 4, 5`)

	return err
}

// pgTextArray renders a Go slice as a Postgres text[] literal. The driver
// handles []string for some paths but not all, and the fixture only ever holds
// simple tag values.
func pgTextArray(vals []string) string {
	if len(vals) == 0 {
		return "{}"
	}

	out := "{"
	for i, v := range vals {
		if i > 0 {
			out += ","
		}
		out += `"` + v + `"`
	}

	return out + "}"
}
