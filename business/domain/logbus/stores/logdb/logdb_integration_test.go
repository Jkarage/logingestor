package logdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/logbus/stores/logdb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

// harness is a migrated database with an org, two projects and a store built on
// top of it. Two projects is the minimum that makes the org-wide fan-out
// meaningful: with one, `project_id = :project_id` and the LATERAL form agree.
type harness struct {
	db       *dbtest.Database
	store    *logdb.Store
	orgID    uuid.UUID
	projects []uuid.UUID
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
		db:       db,
		store:    logdb.NewStore(db.Log, db.DB),
		orgID:    f.OrgID,
		projects: []uuid.UUID{f.ProjectID, second},
	}
}

// insert writes through the store so the rollup is maintained by the production
// path rather than by the test.
func (h harness) insert(t *testing.T, logs ...logbus.Log) {
	t.Helper()

	for i := range logs {
		if logs[i].ID == uuid.Nil {
			logs[i].ID = uuid.New()
		}
		if logs[i].Source == "" {
			logs[i].Source = "api"
		}
		if logs[i].SourceType == "" {
			logs[i].SourceType = logbus.SourceTypeApp
		}
		if logs[i].Message == "" {
			logs[i].Message = "message"
		}
	}

	if err := h.store.BulkInsert(context.Background(), logs); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
}

func minutesAgo(n int) time.Time {
	return time.Now().UTC().Add(-time.Duration(n) * time.Minute).Truncate(time.Millisecond)
}

// The org-wide read replaces `project_id = :project_id` with a LATERAL fan-out
// over a uuid array. A version of that change built the query correctly and
// still returned nothing, because the WHERE builder ignored the predicate it was
// handed — invisible to build, vet and every unit test, since no unit test can
// see generated SQL run against rows.
func Test_LogDB_Integration_OrgWideQueryReturnsEveryProject(t *testing.T) {
	h := newHarness(t)

	h.insert(t,
		logbus.Log{ProjectID: h.projects[0], Level: logbus.LevelInfo, Message: "first project", Timestamp: minutesAgo(3)},
		logbus.Log{ProjectID: h.projects[1], Level: logbus.LevelError, Message: "second project", Timestamp: minutesAgo(2)},
		logbus.Log{ProjectID: h.projects[0], Level: logbus.LevelWarn, Message: "first project again", Timestamp: minutesAgo(1)},
	)

	logs, total, err := h.store.Query(context.Background(), logbus.QueryFilter{ProjectIDs: h.projects}, 10, nil, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(logs) != 3 {
		t.Fatalf("org-wide query returned %d logs, want 3", len(logs))
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	// Newest first, merged across projects rather than concatenated per project.
	wantOrder := []string{"first project again", "second project", "first project"}
	for i, want := range wantOrder {
		if logs[i].Message != want {
			t.Errorf("logs[%d].Message = %q, want %q", i, logs[i].Message, want)
		}
	}

	seen := map[uuid.UUID]int{}
	for _, l := range logs {
		seen[l.ProjectID]++
	}
	for _, p := range h.projects {
		if seen[p] == 0 {
			t.Errorf("no rows returned for project %s", p)
		}
	}
}

// A project outside the requested set must never leak into an org-wide read.
func Test_LogDB_Integration_OrgWideQueryExcludesOtherProjects(t *testing.T) {
	h := newHarness(t)

	other := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO organizations (id, name, slug, created_at, updated_at, date_created, date_updated)
		VALUES ($1, 'other', 'other-org', NOW(), NOW(), NOW(), NOW())`, other); err != nil {
		t.Fatalf("other org: %v", err)
	}

	foreign := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO projects (id, org_id, name, date_created, date_updated)
		VALUES ($1, $2, 'foreign', NOW(), NOW())`, foreign, other); err != nil {
		t.Fatalf("foreign project: %v", err)
	}

	h.insert(t,
		logbus.Log{ProjectID: h.projects[0], Level: logbus.LevelInfo, Message: "mine", Timestamp: minutesAgo(2)},
		logbus.Log{ProjectID: foreign, Level: logbus.LevelInfo, Message: "not mine", Timestamp: minutesAgo(1)},
	)

	logs, total, err := h.store.Query(context.Background(), logbus.QueryFilter{ProjectIDs: h.projects}, 10, nil, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(logs) != 1 || logs[0].Message != "mine" {
		t.Fatalf("query returned %d logs (%v), want only the requested projects", len(logs), messages(logs))
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
}

// Cursor paging over the fan-out must visit every row exactly once. The inner
// per-project LIMIT makes off-by-one errors here silent data loss rather than an
// error.
func Test_LogDB_Integration_OrgWidePagingIsComplete(t *testing.T) {
	h := newHarness(t)

	const want = 11
	logs := make([]logbus.Log, 0, want)
	for i := 0; i < want; i++ {
		logs = append(logs, logbus.Log{
			ProjectID: h.projects[i%len(h.projects)],
			Level:     logbus.LevelInfo,
			Message:   "page me",
			Timestamp: minutesAgo(want - i),
		})
	}
	h.insert(t, logs...)

	seen := map[uuid.UUID]bool{}
	var afterTs *time.Time
	var afterID *uuid.UUID

	for page := 0; page < 10; page++ {
		got, _, err := h.store.Query(context.Background(),
			logbus.QueryFilter{ProjectIDs: h.projects, TotalMode: logbus.TotalNone}, 3, afterTs, afterID)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(got) == 0 {
			break
		}

		for _, l := range got {
			if seen[l.ID] {
				t.Errorf("log %s returned on more than one page", l.ID)
			}
			seen[l.ID] = true
		}

		last := got[len(got)-1]
		ts, id := last.Timestamp, last.ID
		afterTs, afterID = &ts, &id
	}

	if len(seen) != want {
		t.Errorf("paged through %d logs, want %d", len(seen), want)
	}
}

// Every filter is exercised against real rows, because each one is generated SQL
// whose correctness depends on how the driver binds its parameter.
func Test_LogDB_Integration_Filters(t *testing.T) {
	h := newHarness(t)

	p := h.projects[0]
	h.insert(t,
		logbus.Log{ProjectID: p, Level: logbus.LevelError, Message: "payment declined", Source: "billing", Timestamp: minutesAgo(5), Tags: []string{"payments", "urgent"}, Meta: map[string]any{"orderId": "123"}},
		logbus.Log{ProjectID: p, Level: logbus.LevelInfo, Message: "user signed in", Source: "auth", Timestamp: minutesAgo(4), Meta: map[string]any{"orderId": "456"}},
		logbus.Log{ProjectID: p, Level: logbus.LevelWarn, Message: "slow query", Source: "billing", Timestamp: minutesAgo(3), Tags: []string{"payments"}},
		logbus.Log{ProjectID: p, Level: logbus.LevelInfo, Message: "healthcheck", Source: "infra-agent", SourceType: logbus.SourceTypeInfra, Timestamp: minutesAgo(2)},
	)

	str := func(s string) *string { return &s }

	cases := []struct {
		name   string
		filter logbus.QueryFilter
		want   []string
	}{
		{
			name:   "level matches any listed level",
			filter: logbus.QueryFilter{Levels: []logbus.Level{logbus.LevelError, logbus.LevelWarn}},
			want:   []string{"slow query", "payment declined"},
		},
		{
			// The search side is an OR across message and source; both need a
			// trigram index or the pair falls back to a scan.
			name:   "search matches the message",
			filter: logbus.QueryFilter{Search: str("declin")},
			want:   []string{"payment declined"},
		},
		{
			name:   "search also matches the source",
			filter: logbus.QueryFilter{Search: str("auth")},
			want:   []string{"user signed in"},
		},
		{
			name:   "source matches exactly",
			filter: logbus.QueryFilter{Source: str("billing")},
			want:   []string{"slow query", "payment declined"},
		},
		{
			name:   "tags require every listed tag",
			filter: logbus.QueryFilter{Tags: []string{"payments", "urgent"}},
			want:   []string{"payment declined"},
		},
		{
			name:   "a single tag matches every row carrying it",
			filter: logbus.QueryFilter{Tags: []string{"payments"}},
			want:   []string{"slow query", "payment declined"},
		},
		{
			// Containment compares values as JSON, so the string "123" matches
			// and the number 123 would not.
			name:   "meta matches by containment",
			filter: logbus.QueryFilter{Meta: map[string]string{"orderId": "123"}},
			want:   []string{"payment declined"},
		},
		{
			name:   "source type splits app from infra",
			filter: logbus.QueryFilter{SourceType: str(logbus.SourceTypeInfra)},
			want:   []string{"healthcheck"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.filter
			f.ProjectID = p

			// Tag filters are window-bound by the business layer; the store is
			// given an explicit window here so the test states the same bound.
			from := minutesAgo(60)
			f.From = &from

			logs, total, err := h.store.Query(context.Background(), f, 10, nil, nil)
			if err != nil {
				t.Fatalf("query: %v", err)
			}

			got := messages(logs)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
			if total != len(c.want) {
				t.Errorf("total = %d, want %d", total, len(c.want))
			}
		})
	}
}

// Below the cap a bounded count is exact, so the API can report a precise total
// for ordinary projects and only degrade to "10,000+" on large ones.
func Test_LogDB_Integration_TotalModes(t *testing.T) {
	h := newHarness(t)

	p := h.projects[0]
	logs := make([]logbus.Log, 0, 5)
	for i := 0; i < 5; i++ {
		logs = append(logs, logbus.Log{ProjectID: p, Level: logbus.LevelInfo, Timestamp: minutesAgo(i + 1)})
	}
	h.insert(t, logs...)

	for _, c := range []struct {
		name string
		mode logbus.TotalMode
		want int
	}{
		{"bounded is exact below the cap", logbus.TotalBounded, 5},
		{"exact counts every row", logbus.TotalExact, 5},
		{"none skips the count", logbus.TotalNone, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, total, err := h.store.Query(context.Background(),
				logbus.QueryFilter{ProjectID: p, TotalMode: c.mode}, 2, nil, nil)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(got) != 2 {
				t.Errorf("page size = %d, want 2", len(got))
			}
			if total != c.want {
				t.Errorf("total = %d, want %d", total, c.want)
			}
		})
	}
}

// Stats is served from the rollup rather than the logs table. That only holds if
// BulkInsert's deltas land in the same buckets the rollup is keyed by, which is
// a property of real rows and a real UTC truncation.
func Test_LogDB_Integration_StatsComeFromTheRollup(t *testing.T) {
	h := newHarness(t)

	p := h.projects[0]
	h.insert(t,
		logbus.Log{ProjectID: p, Level: logbus.LevelError, Timestamp: minutesAgo(200)},
		logbus.Log{ProjectID: p, Level: logbus.LevelError, Timestamp: minutesAgo(5)},
		logbus.Log{ProjectID: p, Level: logbus.LevelInfo, Timestamp: minutesAgo(5)},
		logbus.Log{ProjectID: p, Level: logbus.LevelInfo, SourceType: logbus.SourceTypeInfra, Timestamp: minutesAgo(5)},
		logbus.Log{ProjectID: h.projects[1], Level: logbus.LevelWarn, Timestamp: minutesAgo(5)},
	)

	counts, err := h.store.Stats(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	want := map[string]int{"DEBUG": 0, "INFO": 2, "WARN": 0, "ERROR": 2}
	for level, n := range want {
		if counts[level] != n {
			t.Errorf("counts[%s] = %d, want %d", level, counts[level], n)
		}
	}

	app := logbus.SourceTypeApp
	appCounts, err := h.store.Stats(context.Background(), p, &app)
	if err != nil {
		t.Fatalf("stats app: %v", err)
	}
	if appCounts["INFO"] != 1 {
		t.Errorf("app INFO = %d, want 1: the infra row must not be counted", appCounts["INFO"])
	}

	// The rollup the stats read from has to agree with the rows it summarises.
	h.db.AssertRollupConverged(t)
}

func messages(logs []logbus.Log) []string {
	out := make([]string, 0, len(logs))
	for _, l := range logs {
		out = append(out, l.Message)
	}

	return out
}
