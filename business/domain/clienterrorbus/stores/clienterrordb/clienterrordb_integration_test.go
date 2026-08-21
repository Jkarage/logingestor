package clienterrordb_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus/stores/clienterrordb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
	"github.com/jkarage/logingestor/business/sdk/retention"
)

type harness struct {
	db      *dbtest.Database
	bus     *clienterrorbus.Business
	fixture dbtest.Fixture
	notes   *recorder
}

// recorder stands in for the alert delivery this domain does not own, so the
// tests can assert what would have been notified.
type recorder struct {
	opened    []clienterrorbus.Issue
	regressed []clienterrorbus.Issue
}

func (r *recorder) IssueOpened(_ context.Context, i clienterrorbus.Issue, _ clienterrorbus.Event) {
	r.opened = append(r.opened, i)
}

func (r *recorder) IssueRegressed(_ context.Context, i clienterrorbus.Issue, _ clienterrorbus.Event) {
	r.regressed = append(r.regressed, i)
}

func newHarness(t *testing.T) harness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")
	notes := &recorder{}

	return harness{
		db:      db,
		bus:     clienterrorbus.NewBusiness(db.Log, clienterrordb.NewStore(db.Log, db.DB), notes),
		fixture: f,
		notes:   notes,
	}
}

// event builds a report. The stack is what grouping keys on, so it is explicit.
func event(message, frame string) clienterrorbus.NewEvent {
	return clienterrorbus.NewEvent{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Level:      clienterrorbus.LevelError,
		Kind:       clienterrorbus.KindReact,
		Name:       "TypeError",
		Message:    message,
		Stack:      fmt.Sprintf("TypeError: %s\n    at %s (https://streamlogia.com/assets/index-64s.js:1:24817)", message, frame),
		Release:    "streamlogia-frontend@1.5.0",
		URL:        "/dashboard/logs",
	}
}

// The whole pipeline: a report is accepted, stored, then grouped by a worker
// into an issue that carries the counts a dashboard needs.
func Test_ClientError_Integration_IngestThenGroup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	who := clienterrorbus.Reporter{UserID: &h.fixture.UserID, OrgID: &h.fixture.OrgID, Role: "ORG ADMIN"}

	n, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{
		event("cannot read properties of undefined (reading 'id')", "LogsView"),
		event("cannot read properties of undefined (reading 'name')", "LogsView"),
		event("network request failed", "fetchLogs"),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 3 {
		t.Fatalf("accepted %d events, want 3", n)
	}

	// Nothing is grouped yet: the request must not do that work.
	issues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query issues before processing: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("ingest created %d issues; grouping belongs to the worker", len(issues))
	}

	processed, err := h.bus.ProcessBatch(ctx, 10)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if processed != 3 {
		t.Fatalf("processed %d events, want 3", processed)
	}

	issues, _, err = h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query issues: %v", err)
	}

	// Two of the three share a fingerprint: same throw site, same message shape.
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2 — the two undefined reads group together", len(issues))
	}

	var grouped clienterrorbus.Issue
	for _, i := range issues {
		if i.EventCount == 2 {
			grouped = i
		}
	}
	if grouped.ID == uuid.Nil {
		t.Fatalf("no issue collected two events: %+v", issues)
	}
	if grouped.AffectedUsers != 1 {
		t.Errorf("affectedUsers = %d, want 1", grouped.AffectedUsers)
	}
	if len(grouped.Releases) != 1 || grouped.Releases[0] != "streamlogia-frontend@1.5.0" {
		t.Errorf("releases = %v", grouped.Releases)
	}
	if grouped.Status != clienterrorbus.StatusUnresolved {
		t.Errorf("status = %q, want unresolved", grouped.Status)
	}

	// A new issue is worth waking someone for; a second event on it is not.
	if len(h.notes.opened) != 2 {
		t.Errorf("notified %d new issues, want 2", len(h.notes.opened))
	}
	if len(h.notes.regressed) != 0 {
		t.Errorf("notified a regression on first sight")
	}

	// The queue is empty, so a second pass does nothing.
	if again, err := h.bus.ProcessBatch(ctx, 10); err != nil || again != 0 {
		t.Errorf("second pass processed %d events (err %v), want 0", again, err)
	}

	events, err := h.bus.QueryIssueEvents(ctx, grouped.ID, 10)
	if err != nil {
		t.Fatalf("query issue events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("issue has %d events, want 2", len(events))
	}
	if events[0].Fingerprint == "" {
		t.Errorf("event was attached without a fingerprint")
	}
}

// sendBeacon fires on unload and a failed flush is retried, so the same event
// arrives more than once. It must be counted once.
func Test_ClientError_Integration_DuplicatesAreIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	e := event("boom", "Widget")

	first, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, []clienterrorbus.NewEvent{e})
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first != 1 {
		t.Fatalf("accepted %d, want 1", first)
	}

	second, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, []clienterrorbus.NewEvent{e, e})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second != 0 {
		t.Errorf("accepted %d duplicates, want 0", second)
	}

	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	issues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].EventCount != 1 {
		t.Errorf("eventCount = %d, want 1: a resent beacon was double counted", issues[0].EventCount)
	}
}

// Resolving an issue and then hitting the same bug again is the case the whole
// status model exists for.
func Test_ClientError_Integration_Regression(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}

	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{event("it broke", "Checkout")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	issues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected one issue, got %d (err %v)", len(issues), err)
	}

	resolved := clienterrorbus.StatusResolved
	updated, err := h.bus.UpdateIssue(ctx, issues[0], clienterrorbus.UpdateIssue{Status: &resolved})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if updated.ResolvedAt == nil {
		t.Errorf("resolvedAt was not stamped")
	}

	// The same bug comes back.
	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{event("it broke", "Checkout")}); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("second process: %v", err)
	}

	reread, err := h.bus.QueryIssueByID(ctx, issues[0].ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.Status != clienterrorbus.StatusUnresolved {
		t.Errorf("status = %q, want unresolved after a regression", reread.Status)
	}
	if !reread.Regressed {
		t.Errorf("regressed flag was not set")
	}
	if reread.ResolvedAt != nil {
		t.Errorf("resolvedAt survived the regression")
	}
	if reread.EventCount != 2 {
		t.Errorf("eventCount = %d, want 2", reread.EventCount)
	}

	if len(h.notes.regressed) != 1 {
		t.Errorf("notified %d regressions, want 1", len(h.notes.regressed))
	}

	// Resolving again is the acknowledgement, so the flag clears.
	if _, err := h.bus.UpdateIssue(ctx, reread, clienterrorbus.UpdateIssue{Status: &resolved}); err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	final, err := h.bus.QueryIssueByID(ctx, issues[0].ID)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final.Regressed {
		t.Errorf("regressed flag survived being resolved again")
	}

	// An ignored issue stays ignored when it fires: ignoring is a standing
	// decision, not a one-time dismissal.
	ignored := clienterrorbus.StatusIgnored
	if _, err := h.bus.UpdateIssue(ctx, final, clienterrorbus.UpdateIssue{Status: &ignored}); err != nil {
		t.Fatalf("ignore: %v", err)
	}
	if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{event("it broke", "Checkout")}); err != nil {
		t.Fatalf("third ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("third process: %v", err)
	}
	afterIgnore, err := h.bus.QueryIssueByID(ctx, issues[0].ID)
	if err != nil {
		t.Fatalf("read after ignore: %v", err)
	}
	if afterIgnore.Status != clienterrorbus.StatusIgnored {
		t.Errorf("status = %q, want ignored to stick", afterIgnore.Status)
	}
}

// Anonymous reports — the crashes on the login page — must be stored, grouped,
// and kept out of every org's view.
func Test_ClientError_Integration_AnonymousIsSeparate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, []clienterrorbus.NewEvent{event("login page broke", "LoginForm")}); err != nil {
		t.Fatalf("anonymous ingest: %v", err)
	}
	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}, []clienterrorbus.NewEvent{event("dashboard broke", "Dashboard")}); err != nil {
		t.Fatalf("org ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	orgIssues, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("org issues: %v", err)
	}
	if len(orgIssues) != 1 || orgIssues[0].Title == "" {
		t.Fatalf("org sees %d issues, want only its own", len(orgIssues))
	}
	for _, i := range orgIssues {
		if i.OrgID == nil {
			t.Errorf("an anonymous issue leaked into an org's list")
		}
	}

	anon, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{})
	if err != nil {
		t.Fatalf("anonymous issues: %v", err)
	}
	if len(anon) != 1 || anon[0].OrgID != nil {
		t.Fatalf("anonymous scope returned %d issues", len(anon))
	}

	all, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{AllOrgs: true})
	if err != nil {
		t.Fatalf("all issues: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("the cross-org view returned %d issues, want 2", len(all))
	}
}

// The same fingerprint from two tenants is two issues, so one org resolving it
// does not close the other's, and a deletion for one cannot remove the other's.
func Test_ClientError_Integration_IssuesAreScopedPerOrg(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	other := h.db.SeedFixture(t, "free")

	same := func() clienterrorbus.NewEvent { return event("shared bug", "SharedView") }

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}, []clienterrorbus.NewEvent{same()}); err != nil {
		t.Fatalf("ingest a: %v", err)
	}
	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &other.OrgID}, []clienterrorbus.NewEvent{same()}); err != nil {
		t.Fatalf("ingest b: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	a, _, _ := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	b, _, _ := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &other.OrgID})

	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("issues per org: %d and %d, want 1 each", len(a), len(b))
	}
	if a[0].ID == b[0].ID {
		t.Fatalf("two orgs share an issue row")
	}
	if a[0].Fingerprint != b[0].Fingerprint {
		t.Errorf("the same bug produced different fingerprints")
	}

	// A deletion request for one org leaves the other untouched.
	if _, err := h.bus.PurgeOrg(ctx, h.fixture.OrgID); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if left, _, _ := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID}); len(left) != 0 {
		t.Errorf("purge left %d issues", len(left))
	}
	if kept, _, _ := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &other.OrgID}); len(kept) != 1 {
		t.Errorf("purge removed another org's issues")
	}

	var events int
	if err := h.db.DB.Get(&events, `SELECT count(1) FROM client_error_events WHERE org_id = $1`, h.fixture.OrgID); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 0 {
		t.Errorf("%d events survived the purge", events)
	}
}

// A report the fingerprinter chokes on must not stop the queue behind it. There
// is no such report today, so the condition is forced by breaking the row.
func Test_ClientError_Integration_PoisonEventDoesNotStallTheQueue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, []clienterrorbus.NewEvent{
		event("first", "A"), event("second", "B"),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Point one event at an issue id that cannot exist, so attaching it fails the
	// foreign key every time.
	if _, err := h.db.DB.Exec(`
		UPDATE client_error_events SET process_attempts = 0
		WHERE message = 'first'`); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := h.db.DB.Exec(`
		ALTER TABLE client_error_events
		ADD CONSTRAINT tmp_poison CHECK (message <> 'first' OR issue_id IS NULL) NOT VALID`); err != nil {
		t.Fatalf("add poison constraint: %v", err)
	}

	// Every pass makes progress on the healthy event and retries the poisoned one
	// until its attempts run out.
	for i := 0; i < 4; i++ {
		if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	var healthy int
	if err := h.db.DB.Get(&healthy, `
		SELECT count(1) FROM client_error_events
		WHERE message = 'second' AND issue_id IS NOT NULL AND processed_at IS NOT NULL`); err != nil {
		t.Fatalf("count healthy: %v", err)
	}
	if healthy != 1 {
		t.Errorf("the healthy event was not grouped; one bad row stalled the queue")
	}

	var poison struct {
		Attempts  int     `db:"process_attempts"`
		Processed *string `db:"processed_at"`
		Reason    *string `db:"process_error"`
	}
	if err := h.db.DB.Get(&poison, `
		SELECT process_attempts, processed_at::text, process_error
		FROM client_error_events WHERE message = 'first'`); err != nil {
		t.Fatalf("read poison row: %v", err)
	}
	if poison.Reason == nil || *poison.Reason == "" {
		t.Errorf("no reason was recorded for the failure")
	}
	if poison.Processed == nil {
		t.Errorf("the poisoned event is still claimable after %d attempts", poison.Attempts)
	}

	if _, err := h.db.DB.Exec(`ALTER TABLE client_error_events DROP CONSTRAINT tmp_poison`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

// Sorting and paging are what make a list of issues usable when there are
// hundreds.
func Test_ClientError_Integration_SortingAndPaging(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}

	// Three issues with 3, 2 and 1 events.
	for i, frame := range []string{"Heavy", "Medium", "Light"} {
		batch := make([]clienterrorbus.NewEvent, 0, 3-i)
		for j := 0; j < 3-i; j++ {
			batch = append(batch, event("failure in "+frame, frame))
		}
		if _, err := h.bus.Ingest(ctx, who, batch); err != nil {
			t.Fatalf("ingest %s: %v", frame, err)
		}
	}
	if _, err := h.bus.ProcessBatch(ctx, 50); err != nil {
		t.Fatalf("process: %v", err)
	}

	byCount, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{
		OrgID: &h.fixture.OrgID, Sort: clienterrorbus.SortCount,
	})
	if err != nil {
		t.Fatalf("sort by count: %v", err)
	}
	if len(byCount) != 3 {
		t.Fatalf("got %d issues, want 3", len(byCount))
	}
	for i := 1; i < len(byCount); i++ {
		if byCount[i-1].EventCount < byCount[i].EventCount {
			t.Errorf("count sort is not descending: %v", []int64{byCount[i-1].EventCount, byCount[i].EventCount})
		}
	}

	// Paging must cover every issue exactly once.
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		got, next, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{
			OrgID: &h.fixture.OrgID, Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, i := range got {
			if seen[i.ID] {
				t.Errorf("issue %s appeared on two pages", i.ID)
			}
			seen[i.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 3 {
		t.Errorf("paged over %d issues, want 3", len(seen))
	}

	// A status filter narrows, and the stats tiles agree with the list.
	stats, err := h.bus.QueryStats(ctx, &h.fixture.OrgID, nil, false, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Events != 6 {
		t.Errorf("stats events = %d, want 6", stats.Events)
	}
	if stats.Issues != 3 || stats.NewIssues != 3 || stats.Unresolved != 3 {
		t.Errorf("stats = %+v, want 3 issues all new and unresolved", stats)
	}
}

// Retention has to age the bulk out without deleting an issue that still has
// events pointing at it.
func Test_ClientError_Integration_Retention(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}, []clienterrorbus.NewEvent{
		event("old news", "Old"), event("today", "New"),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	// Age one event and its issue past every window.
	if _, err := h.db.DB.Exec(`
		UPDATE client_error_events SET received_at = NOW() - interval '200 days'
		WHERE message = 'old news'`); err != nil {
		t.Fatalf("age event: %v", err)
	}
	if _, err := h.db.DB.Exec(`
		UPDATE client_error_issues SET last_seen_at = NOW() - interval '200 days'
		WHERE title LIKE '%old news%'`); err != nil {
		t.Fatalf("age issue: %v", err)
	}

	res, err := retention.Run(ctx, h.db.Log, h.db.DB, retention.Config{
		ClientErrorEventDays: 30, ClientErrorIssueDays: 180,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.ClientErrorEventsDeleted != 1 {
		t.Errorf("deleted %d events, want 1", res.ClientErrorEventsDeleted)
	}
	if res.ClientErrorIssuesDeleted != 1 {
		t.Errorf("deleted %d issues, want 1", res.ClientErrorIssuesDeleted)
	}
	if res.Total() != 0 {
		t.Errorf("Total() = %d: our own diagnostics must not consume the log budget", res.Total())
	}

	left, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID, Since: ptrTime(time.Now().Add(-time.Hour))})
	if err != nil {
		t.Fatalf("query after retention: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d issues remain, want the recent one", len(left))
	}

	// Negative keeps everything, which is the escape hatch for an investigation.
	if _, err := h.db.DB.Exec(`UPDATE client_error_events SET received_at = NOW() - interval '400 days'`); err != nil {
		t.Fatalf("age everything: %v", err)
	}
	res, err = retention.Run(ctx, h.db.Log, h.db.DB, retention.Config{ClientErrorEventDays: -1, ClientErrorIssueDays: -1})
	if err != nil {
		t.Fatalf("run with keep-forever: %v", err)
	}
	if res.ClientErrorEventsDeleted != 0 {
		t.Errorf("keep-forever deleted %d events", res.ClientErrorEventsDeleted)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// Ingest rejects a batch that is too large, and says so with a domain error
// rather than storing part of it.
func Test_ClientError_Integration_BatchLimits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	too := make([]clienterrorbus.NewEvent, clienterrorbus.MaxBatchEvents+1)
	for i := range too {
		too[i] = event("flood", "F")
	}

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, too); !errors.Is(err, clienterrorbus.ErrTooManyEvents) {
		t.Errorf("err = %v, want ErrTooManyEvents", err)
	}
	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, nil); !errors.Is(err, clienterrorbus.ErrNoEvents) {
		t.Errorf("err = %v, want ErrNoEvents", err)
	}

	var stored int
	if err := h.db.DB.Get(&stored, `SELECT count(1) FROM client_error_events`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stored != 0 {
		t.Errorf("%d events were stored from rejected batches", stored)
	}
}

// Storage must hold what the API promises: no PII, and every field bounded.
func Test_ClientError_Integration_ScrubbingIsEnforcedAtTheStore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	dirty := clienterrorbus.NewEvent{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Level:      clienterrorbus.LevelError,
		Kind:       clienterrorbus.KindAPI,
		Name:       "ApiError",
		Message:    "failed for joseph@bsa.ai with Bearer abc123def456ghi789",
		Stack:      "at request (https://streamlogia.com/app.js?token=secret123456:1:1)",
		URL:        "/dashboard/logs?token=supersecret123&level=ERROR",
		API:        &clienterrorbus.APIContext{Method: "GET", Path: "/v1/logs?token=leaky123456", Status: 401, Code: "unauthenticated"},
		Breadcrumbs: []clienterrorbus.Breadcrumb{
			{TS: time.Now().UTC(), Category: "fetch", Message: "GET /v1/orgs?token=alsoleaky123 401"},
		},
	}

	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{}, []clienterrorbus.NewEvent{dirty}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	// Read the whole row back as text and look for anything that should be gone.
	var blob string
	if err := h.db.DB.Get(&blob, `
		SELECT concat_ws(' ', message, stack, url, api::text, breadcrumbs::text)
		FROM client_error_events LIMIT 1`); err != nil {
		t.Fatalf("read row: %v", err)
	}

	for _, secret := range []string{
		"joseph@bsa.ai", "abc123def456ghi789", "secret123456",
		"supersecret123", "leaky123456", "alsoleaky123",
	} {
		if strings.Contains(blob, secret) {
			t.Errorf("stored row still contains %q: %s", secret, blob)
		}
	}
}

// The same bug in two projects of one org is two issues, because two teams will
// fix it separately and each has its own alert rules.
func Test_ClientError_Integration_IssuesAreScopedPerProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	second := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO projects (id, org_id, name, date_created, date_updated)
		VALUES ($1, $2, 'second', NOW(), NOW())`, second, h.fixture.OrgID); err != nil {
		t.Fatalf("second project: %v", err)
	}

	for _, project := range []uuid.UUID{h.fixture.ProjectID, second} {
		p := project
		if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &p},
			[]clienterrorbus.NewEvent{event("shared bug", "SharedView")}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	all, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d issues, want one per project", len(all))
	}
	if all[0].Fingerprint != all[1].Fingerprint {
		t.Errorf("the same bug produced different fingerprints")
	}

	// And the filter narrows to one of them.
	one, _, err := h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID, ProjectID: &second})
	if err != nil {
		t.Fatalf("query by project: %v", err)
	}
	if len(one) != 1 || one[0].ProjectID == nil || *one[0].ProjectID != second {
		t.Errorf("project filter returned %d issues", len(one))
	}

	// A report with no project is a third issue, not folded into either: it is a
	// crash we cannot attribute, and pretending otherwise would page a team for
	// something that may not be theirs.
	if _, err := h.bus.Ingest(ctx, clienterrorbus.Reporter{OrgID: &h.fixture.OrgID},
		[]clienterrorbus.NewEvent{event("shared bug", "SharedView")}); err != nil {
		t.Fatalf("ingest without a project: %v", err)
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	all, _, err = h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query again: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d issues, want three scopes", len(all))
	}

	// Deleting a project takes its issues, and leaves the others.
	if _, err := h.db.DB.Exec(`DELETE FROM projects WHERE id = $1`, second); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	all, _, err = h.bus.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("query after delete: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d issues after deleting a project, want 2", len(all))
	}
}

// Only an issue with a project can alert, since that is where rules live.
func Test_ClientError_Integration_NotifiesOnlyWithAProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Anonymous, then org-only, then project-scoped: three distinct issues.
	batches := []clienterrorbus.Reporter{
		{},
		{OrgID: &h.fixture.OrgID},
		{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID},
	}
	for i, who := range batches {
		if _, err := h.bus.Ingest(ctx, who, []clienterrorbus.NewEvent{event(fmt.Sprintf("scope %d", i), "View")}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	if _, err := h.bus.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	// The recorder is told about all three; it is the delivery adapter that
	// declines the ones with no project, and that decision is asserted in the
	// clientalert tests.
	if len(h.notes.opened) != 3 {
		t.Fatalf("notified %d new issues, want 3", len(h.notes.opened))
	}

	var withProject int
	for _, i := range h.notes.opened {
		if i.ProjectID != nil {
			withProject++
			if *i.ProjectID != h.fixture.ProjectID {
				t.Errorf("issue carried the wrong project: %s", i.ProjectID)
			}
		}
	}
	if withProject != 1 {
		t.Errorf("%d notified issues carry a project, want 1", withProject)
	}
}
