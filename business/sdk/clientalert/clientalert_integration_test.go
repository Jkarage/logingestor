package clientalert_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus/stores/clienterrordb"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus/stores/integrationdb"
	"github.com/jkarage/logingestor/business/sdk/clientalert"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// recorder is a provider that records deliveries instead of making them.
type recorder struct {
	mu   sync.Mutex
	sent []integrationbus.AlertPayload
}

func (r *recorder) Send(_ context.Context, _ map[string]string, p integrationbus.AlertPayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sent = append(r.sent, p)

	return nil
}

func (r *recorder) delivered() []integrationbus.AlertPayload {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]integrationbus.AlertPayload(nil), r.sent...)
}

type harness struct {
	db       *dbtest.Database
	errors   *clienterrorbus.Business
	alerts   *integrationbus.Business
	provider *recorder
	fixture  dbtest.Fixture
}

func newHarness(t *testing.T) harness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	provider := &recorder{}
	alerts := integrationbus.NewBusiness(db.Log, integrationdb.NewStore(db.Log, db.DB, testKey),
		map[string]integrationbus.Caller{"slack": provider})

	errors := clienterrorbus.NewBusiness(db.Log, clienterrordb.NewStore(db.Log, db.DB),
		clientalert.New(db.Log, alerts))

	return harness{db: db, errors: errors, alerts: alerts, provider: provider, fixture: f}
}

// rule creates a connection and an alert rule on the fixture's project.
func (h harness) rule(t *testing.T, level string, condition integrationbus.Condition) integrationbus.AlertRule {
	t.Helper()

	ctx := context.Background()

	conn, err := h.alerts.Create(ctx, h.fixture.UserID, integrationbus.NewIntegration{
		OrgID:       h.fixture.OrgID,
		ProjectID:   h.fixture.ProjectID,
		ProviderID:  "slack",
		Name:        "primary-" + uuid.NewString()[:8],
		Credentials: map[string]string{"webhookUrl": "https://hooks.example.test/x"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	r, err := h.alerts.CreateRule(ctx, integrationbus.NewAlertRule{
		OrgID:        h.fixture.OrgID,
		ProjectID:    h.fixture.ProjectID,
		ConnectionID: conn.ID,
		UserID:       h.fixture.UserID,
		Name:         "frontend",
		Level:        level,
		Condition:    condition,
		IsActive:     true,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	return r
}

func crash(message string) clienterrorbus.NewEvent {
	return clienterrorbus.NewEvent{
		EventID:    uuid.New(),
		OccurredAt: time.Now().UTC(),
		Level:      clienterrorbus.LevelError,
		Kind:       clienterrorbus.KindReact,
		Name:       "TypeError",
		Message:    message,
		Stack:      "TypeError: " + message + "\n    at LogsView (https://streamlogia.com/assets/index-64s.js:1:24817)",
		URL:        "/dashboard/logs",
		Release:    "streamlogia-frontend@1.5.0",
	}
}

// The whole point of scoping client errors to a project: a new issue is
// delivered by that project's existing rules, through its existing channel,
// with no second delivery path to configure.
func Test_ClientAlert_Integration_NewIssueFiresTheProjectsRule(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("checkout is broken")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	sent := h.provider.delivered()
	if len(sent) != 1 {
		t.Fatalf("delivered %d alerts, want 1", len(sent))
	}

	p := sent[0]
	if p.Source != clientalert.Source {
		t.Errorf("source = %q, want %q so a rule can tell these apart", p.Source, clientalert.Source)
	}
	if p.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR", p.Level)
	}
	if !strings.Contains(p.Message, "New client error") {
		t.Errorf("message does not say what happened: %q", p.Message)
	}
	if !strings.Contains(p.Message, "checkout is broken") {
		t.Errorf("message does not carry the error: %q", p.Message)
	}
	if !strings.Contains(p.Message, "/dashboard/logs") {
		t.Errorf("message does not say where it happened: %q", p.Message)
	}

	// It is also in the alert history, so "why was I paged" is answerable from
	// the same place as every other alert.
	events, err := h.alerts.QueryAlertHistory(ctx, integrationbus.AlertEventFilter{OrgID: h.fixture.OrgID})
	if err != nil {
		t.Fatalf("alert history: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("alert history has %d entries, want 1", len(events))
	}

	// A second event on the same issue is not a second page: the issue is not
	// new any more, and the dedup window governs the rest.
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("checkout is broken")}); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if got := len(h.provider.delivered()); got != 1 {
		t.Errorf("delivered %d alerts after a repeat, want 1", got)
	}
}

// A resolved issue coming back is the case worth waking someone for a second
// time, and it is delivered as a regression rather than as a new issue.
func Test_ClientAlert_Integration_RegressionFires(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}

	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("it broke")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	issues, _, err := h.errors.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected one issue, got %d (err %v)", len(issues), err)
	}

	resolved := clienterrorbus.StatusResolved
	if _, err := h.errors.UpdateIssue(ctx, issues[0], clienterrorbus.UpdateIssue{Status: &resolved}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Somebody resolves an issue days after being paged for it, not milliseconds,
	// so the rule's dedup window has long expired by the time it comes back. The
	// test compresses that timeline by backdating when the rule last notified —
	// without it the regression is correctly suppressed as a repeat, which is the
	// dedup window doing its job rather than a bug.
	if _, err := h.db.DB.Exec(`
		UPDATE alert_events SET last_notified_at = NOW() - interval '2 hours'`); err != nil {
		t.Fatalf("backdate the last notification: %v", err)
	}

	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("it broke")}); err != nil {
		t.Fatalf("regression ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("regression process: %v", err)
	}

	sent := h.provider.delivered()
	if len(sent) != 2 {
		t.Fatalf("delivered %d alerts, want the first sighting and the regression", len(sent))
	}
	if !strings.Contains(sent[1].Message, "regressed") {
		t.Errorf("the second alert does not say it is a regression: %q", sent[1].Message)
	}
}

// A rule can name the source, which is how a team that wants frontend crashes
// separated from log alerts does it — and how one that wants nothing to do with
// them opts out.
func Test_ClientAlert_Integration_RulesCanSelectBySource(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// This rule matches only client errors.
	h.rule(t, "ERROR", integrationbus.Condition{
		Type:  integrationbus.ConditionMatch,
		Query: &integrationbus.Query{Source: clientalert.Source},
	})

	// This one matches only log events, so a frontend crash must not reach it.
	h.rule(t, "ERROR", integrationbus.Condition{
		Type:  integrationbus.ConditionMatch,
		Query: &integrationbus.Query{Source: "billing"},
	})

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("only the source rule")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	if got := len(h.provider.delivered()); got != 1 {
		t.Errorf("delivered %d alerts, want only the source-matched rule to fire", got)
	}
}

// An issue with no project has no rules to run through. It must still be
// recorded and triageable, and it must not fall back to some other project's
// channel — waking the wrong team is worse than not waking anyone.
func Test_ClientAlert_Integration_NoProjectMeansNoDelivery(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	for _, who := range []clienterrorbus.Reporter{{}, {OrgID: &h.fixture.OrgID}} {
		if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("nobody owns this")}); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	if got := len(h.provider.delivered()); got != 0 {
		t.Errorf("delivered %d alerts for issues with no project", got)
	}

	// Recorded all the same.
	anon, _, err := h.errors.QueryIssues(ctx, clienterrorbus.IssueFilter{})
	if err != nil {
		t.Fatalf("anonymous issues: %v", err)
	}
	orgOnly, _, err := h.errors.QueryIssues(ctx, clienterrorbus.IssueFilter{OrgID: &h.fixture.OrgID})
	if err != nil {
		t.Fatalf("org issues: %v", err)
	}
	if len(anon) != 1 || len(orgOnly) != 1 {
		t.Errorf("unattributed issues were not recorded: %d anonymous, %d org", len(anon), len(orgOnly))
	}
}

// A maintenance window silences client errors like anything else, which is the
// whole reason for reusing the existing machinery.
func Test_ClientAlert_Integration_RespectsMaintenanceWindows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	project := h.fixture.ProjectID
	if _, err := h.alerts.CreateMaintenanceWindow(ctx, h.fixture.OrgID, h.fixture.UserID, &project,
		"deploying the frontend", time.Now().Add(-time.Minute), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create maintenance window: %v", err)
	}

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &project}
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{crash("during a deploy")}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}

	if got := len(h.provider.delivered()); got != 0 {
		t.Errorf("delivered %d alerts during a maintenance window", got)
	}

	// Suppressed, not lost: it is in the alert history with no notification time,
	// which is how "it fired but nobody was paged" stays answerable.
	events, err := h.alerts.QueryAlertHistory(ctx, integrationbus.AlertEventFilter{OrgID: h.fixture.OrgID})
	if err != nil {
		t.Fatalf("alert history: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("alert history has %d entries, want the suppressed firing", len(events))
	}
	if events[0].LastNotifiedAt != nil {
		t.Errorf("a suppressed alert was marked as notified")
	}
}

// A warning-level crash maps to WARN, so a rule written for warnings means the
// same thing for client errors as it does for logs.
func Test_ClientAlert_Integration_LevelMapping(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// A rule that only fires on ERROR must not fire for a warning.
	h.rule(t, "ERROR", integrationbus.Condition{Type: integrationbus.ConditionLevel, Level: "ERROR"})

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}

	warning := crash("just a warning")
	warning.Level = clienterrorbus.LevelWarning
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{warning}); err != nil {
		t.Fatalf("ingest warning: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process: %v", err)
	}
	if got := len(h.provider.delivered()); got != 0 {
		t.Fatalf("an ERROR rule fired for a warning: %d deliveries", got)
	}

	fatal := crash("the page is gone")
	fatal.Level = clienterrorbus.LevelFatal
	if _, err := h.errors.Ingest(ctx, who, []clienterrorbus.NewEvent{fatal}); err != nil {
		t.Fatalf("ingest fatal: %v", err)
	}
	if _, err := h.errors.ProcessBatch(ctx, 10); err != nil {
		t.Fatalf("process fatal: %v", err)
	}

	sent := h.provider.delivered()
	if len(sent) != 1 {
		t.Fatalf("delivered %d alerts, want the fatal one", len(sent))
	}
	if sent[0].Level != "ERROR" {
		t.Errorf("fatal mapped to %q, want ERROR — the rules have four levels", sent[0].Level)
	}
}

// A spike is the third trigger, and it delivers through the same rule and
// channel as the other two.
func Test_ClientAlert_Integration_SpikeFires(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	cfg := clienterrorbus.SpikeConfig{
		Window:     10 * time.Minute,
		Baseline:   time.Hour,
		Multiplier: 5,
		MinEvents:  25,
	}
	baselineStart, windowStart, _ := clienterrorbus.SpikeBounds(cfg, time.Now().UTC())

	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}

	ingest := func(at time.Time, n int) {
		t.Helper()

		batch := make([]clienterrorbus.NewEvent, 0, n)
		for i := 0; i < n; i++ {
			e := crash("the rate is climbing")
			e.OccurredAt = at
			batch = append(batch, e)
		}

		for start := 0; start < len(batch); start += clienterrorbus.MaxBatchEvents {
			end := min(start+clienterrorbus.MaxBatchEvents, len(batch))
			if _, err := h.errors.Ingest(ctx, who, batch[start:end]); err != nil {
				t.Fatalf("ingest: %v", err)
			}
		}

		for {
			processed, err := h.errors.ProcessBatch(ctx, 200)
			if err != nil {
				t.Fatalf("process: %v", err)
			}
			if processed == 0 {
				break
			}
		}
	}

	// A trickle through the baseline hour, then a burst.
	ingest(baselineStart.Add(time.Minute), 2)
	ingest(windowStart.Add(time.Minute), 80)

	// The first sighting already delivered one alert; clear the slate so what
	// follows is unambiguous, and move the notification out of the dedup window
	// the way an hour of real time would.
	if _, err := h.db.DB.Exec(`UPDATE alert_events SET last_notified_at = NOW() - interval '2 hours'`); err != nil {
		t.Fatalf("backdate the last notification: %v", err)
	}
	before := len(h.provider.delivered())

	// The issue has to be known rather than brand new, which is what makes this a
	// spike and not the new-issue alert again.
	if _, err := h.db.DB.Exec(`UPDATE client_error_issues SET first_seen_at = $1`, baselineStart); err != nil {
		t.Fatalf("backdate the issue: %v", err)
	}

	found, err := h.errors.EvaluateSpikes(ctx, cfg)
	if err != nil {
		t.Fatalf("evaluate spikes: %v", err)
	}
	if found != 1 {
		t.Fatalf("found %d spikes, want 1", found)
	}

	sent := h.provider.delivered()
	if len(sent) != before+1 {
		t.Fatalf("delivered %d alerts, want one more than %d", len(sent), before)
	}

	p := sent[len(sent)-1]
	if p.Source != clientalert.Source {
		t.Errorf("source = %q, want %q", p.Source, clientalert.Source)
	}
	if !strings.Contains(p.Message, "spiking") {
		t.Errorf("message does not say what happened: %q", p.Message)
	}
	// The numbers are the alert: "spiking" without them tells nobody whether to
	// stop the rollout.
	if !strings.Contains(p.Message, "80 in the last 10m") {
		t.Errorf("message does not carry the rate: %q", p.Message)
	}
	if !strings.Contains(p.Message, "×") {
		t.Errorf("message does not carry the multiple: %q", p.Message)
	}
}

// An issue with no project cannot alert, spikes included — there are no rules to
// run through and no team it belongs to.
func Test_ClientAlert_Integration_SpikeNeedsAProject(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.rule(t, "ERROR", integrationbus.Condition{})

	cfg := clienterrorbus.SpikeConfig{Window: 10 * time.Minute, Baseline: time.Hour, Multiplier: 5, MinEvents: 25}
	_, windowStart, _ := clienterrorbus.SpikeBounds(cfg, time.Now().UTC())

	// Org-scoped, no project.
	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID}

	batch := make([]clienterrorbus.NewEvent, 0, 50)
	for i := 0; i < 50; i++ {
		e := crash("unattributed burst")
		e.OccurredAt = windowStart.Add(time.Minute)
		batch = append(batch, e)
	}
	if _, err := h.errors.Ingest(ctx, who, batch); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	for {
		processed, err := h.errors.ProcessBatch(ctx, 200)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if processed == 0 {
			break
		}
	}

	if _, err := h.db.DB.Exec(`UPDATE client_error_issues SET first_seen_at = NOW() - interval '2 days'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	before := len(h.provider.delivered())

	found, err := h.errors.EvaluateSpikes(ctx, cfg)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if found != 1 {
		t.Fatalf("found %d spikes, want the detector to see it", found)
	}
	if got := len(h.provider.delivered()); got != before {
		t.Errorf("delivered %d alerts for an unattributed spike, want %d", got, before)
	}
}
