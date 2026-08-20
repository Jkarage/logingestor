package integrationdb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus/stores/integrationdb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

// testKey is the AES-256 key used to encrypt connection credentials.
var testKey = []byte("0123456789abcdef0123456789abcdef")

// recorder is a provider that records deliveries instead of making them.
type recorder struct {
	mu   sync.Mutex
	sent []integrationbus.AlertPayload
	err  error
}

func (r *recorder) Send(_ context.Context, _ map[string]string, p integrationbus.AlertPayload) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, p)

	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.sent)
}

// stubCounter returns a scripted match count, standing in for the log store. The
// counting query is exercised by the log store's own tests; what matters here is
// the decision the evaluator makes with the number.
type stubCounter struct{ n int }

func (s *stubCounter) CountMatching(context.Context, uuid.UUID, integrationbus.Query, time.Time, time.Time) (int, error) {
	return s.n, nil
}

type alertHarness struct {
	db       *dbtest.Database
	bus      *integrationbus.Business
	store    *integrationdb.Store
	provider *recorder
	fixture  dbtest.Fixture
	connID   uuid.UUID
}

func newAlertHarness(t *testing.T) alertHarness {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	provider := &recorder{}
	store := integrationdb.NewStore(db.Log, db.DB, testKey)
	bus := integrationbus.NewBusiness(db.Log, store, map[string]integrationbus.Caller{"slack": provider})

	// Created through the business API so the credentials are really encrypted
	// and SendAlert can decrypt them again.
	conn, err := bus.Create(context.Background(), f.UserID, integrationbus.NewIntegration{
		OrgID:       f.OrgID,
		ProjectID:   f.ProjectID,
		ProviderID:  "slack",
		Name:        "primary",
		Credentials: map[string]string{"webhookUrl": "https://hooks.example.test/x"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	return alertHarness{db: db, bus: bus, store: store, provider: provider, fixture: f, connID: conn.ID}
}

func (h alertHarness) rule(t *testing.T, nr integrationbus.NewAlertRule) integrationbus.AlertRule {
	t.Helper()

	nr.OrgID = h.fixture.OrgID
	nr.ProjectID = h.fixture.ProjectID
	nr.ConnectionID = h.connID
	nr.UserID = h.fixture.UserID
	if nr.Name == "" {
		nr.Name = "rule"
	}
	if nr.Level == "" {
		nr.Level = "ERROR"
	}
	nr.IsActive = true

	r, err := h.bus.CreateRule(context.Background(), nr)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	return r
}

func (h alertHarness) history(t *testing.T, f integrationbus.AlertEventFilter) []integrationbus.AlertEvent {
	t.Helper()

	if f.OrgID == uuid.Nil {
		f.OrgID = h.fixture.OrgID
	}
	if f.Limit == 0 {
		f.Limit = 50
	}

	events, err := h.bus.QueryAlertHistory(context.Background(), f)
	if err != nil {
		t.Fatalf("query history: %v", err)
	}

	return events
}

func payload(level, message string) integrationbus.AlertPayload {
	return integrationbus.AlertPayload{
		ProjectName: "project",
		Level:       level,
		Message:     message,
		Source:      "api",
		Timestamp:   time.Now(),
	}
}

// A condition submitted on create has to reach the database. It was dropped
// once: the rule was stored with its level only, so the rule silently behaved
// differently from what was configured.
func Test_Alerting_Integration_ConditionRoundTrips(t *testing.T) {
	h := newAlertHarness(t)

	cond := integrationbus.Condition{
		Type:          integrationbus.ConditionThreshold,
		Query:         &integrationbus.Query{Levels: []string{"ERROR"}, Contains: "timeout"},
		WindowSeconds: 300,
		Count:         20,
		Comparator:    integrationbus.ComparatorGTE,
	}

	created := h.rule(t, integrationbus.NewAlertRule{Condition: cond, DedupWindowSeconds: 900})

	stored, err := h.store.QueryRuleByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("query rule: %v", err)
	}

	if stored.Condition.Type != integrationbus.ConditionThreshold {
		t.Fatalf("stored condition type = %q, want %q", stored.Condition.Type, integrationbus.ConditionThreshold)
	}
	if stored.Condition.Count != 20 || stored.Condition.WindowSeconds != 300 {
		t.Errorf("stored threshold = %d in %ds, want 20 in 300s", stored.Condition.Count, stored.Condition.WindowSeconds)
	}
	if stored.Condition.Query == nil || stored.Condition.Query.Contains != "timeout" {
		t.Errorf("stored query = %+v, want contains=timeout", stored.Condition.Query)
	}
	if stored.DedupWindowSeconds != 900 {
		t.Errorf("stored dedup window = %d, want 900", stored.DedupWindowSeconds)
	}

	// A rule created without a condition keeps the pre-condition behaviour.
	plain := h.rule(t, integrationbus.NewAlertRule{Name: "legacy", Level: "WARN"})
	if plain.Condition.Type != integrationbus.ConditionLevel || plain.Condition.Level != "WARN" {
		t.Errorf("default condition = %+v, want a WARN level condition", plain.Condition)
	}
}

// Repeat firings collapse onto one open alert. The partial unique index is what
// enforces this, so it only holds against a real database.
func Test_Alerting_Integration_RepeatsFoldOntoOneOpenAlert(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	h.rule(t, integrationbus.NewAlertRule{Level: "ERROR"})

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{
		payload("ERROR", "disk full"),
		payload("ERROR", "disk still full"),
	}); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{
		payload("ERROR", "disk full again"),
	}); err != nil {
		t.Fatalf("second batch: %v", err)
	}

	events := h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events, want 1 open alert with both firings folded in", len(events))
	}

	e := events[0]
	if e.State != "firing" {
		t.Errorf("state = %q, want firing", e.State)
	}
	if e.MatchCount != 3 {
		t.Errorf("match_count = %d, want 3 (2 + 1 across two batches)", e.MatchCount)
	}

	// One notification: the second batch lands inside the dedup window, which is
	// the whole point of folding.
	if got := h.provider.count(); got != 1 {
		t.Errorf("delivered %d notifications, want 1 within the dedup window", got)
	}

	// Resolving frees the dedup key, so the next firing is a new alert rather
	// than a reopened stale one.
	if _, err := h.bus.ResolveAlert(ctx, e.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{
		payload("ERROR", "disk full once more"),
	}); err != nil {
		t.Fatalf("third batch: %v", err)
	}

	events = h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 2 {
		t.Fatalf("history has %d events, want 2 after resolving the first", len(events))
	}
	if h.provider.count() != 2 {
		t.Errorf("delivered %d notifications, want 2: a new alert notifies immediately", h.provider.count())
	}
}

// A firing is recorded even when it is suppressed, so history answers "it fired
// but nobody was paged".
func Test_Alerting_Integration_SuppressionStillRecordsHistory(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	project := h.fixture.ProjectID
	if _, err := h.bus.CreateMaintenanceWindow(ctx, h.fixture.OrgID, h.fixture.UserID, &project,
		"deploy", time.Now().Add(-time.Minute), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create maintenance window: %v", err)
	}

	h.rule(t, integrationbus.NewAlertRule{Level: "ERROR"})

	if err := h.bus.FireAlerts(ctx, project, []integrationbus.AlertPayload{payload("ERROR", "during a deploy")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	if got := h.provider.count(); got != 0 {
		t.Errorf("delivered %d notifications during a maintenance window, want 0", got)
	}

	events := h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events, want the suppressed firing recorded", len(events))
	}
	if events[0].LastNotifiedAt != nil {
		t.Errorf("last_notified_at is set on a suppressed firing")
	}

	// Once the window is gone the same rule notifies again.
	windows, err := h.bus.QueryMaintenanceWindows(ctx, h.fixture.OrgID)
	if err != nil {
		t.Fatalf("query windows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("got %d maintenance windows, want 1", len(windows))
	}
	if err := h.bus.DeleteMaintenanceWindow(ctx, windows[0].ID); err != nil {
		t.Fatalf("delete window: %v", err)
	}

	if err := h.bus.FireAlerts(ctx, project, []integrationbus.AlertPayload{payload("ERROR", "after the deploy")}); err != nil {
		t.Fatalf("fire after window: %v", err)
	}
	if got := h.provider.count(); got != 1 {
		t.Errorf("delivered %d notifications after the window closed, want 1", got)
	}
}

// A threshold rule fires from the evaluator, resolves when the rate drops, and
// opens a fresh alert on the next breach.
func Test_Alerting_Integration_ThresholdFiresAndResolves(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	h.rule(t, integrationbus.NewAlertRule{
		Name: "error rate",
		Condition: integrationbus.Condition{
			Type:          integrationbus.ConditionThreshold,
			Query:         &integrationbus.Query{Levels: []string{"ERROR"}},
			WindowSeconds: 300,
			Count:         10,
			Comparator:    integrationbus.ComparatorGTE,
		},
	})

	// Under the threshold: nothing fires and nothing is recorded.
	counter := &stubCounter{n: 9}
	fired, err := h.bus.EvaluateThresholds(ctx, counter)
	if err != nil {
		t.Fatalf("evaluate under threshold: %v", err)
	}
	if fired != 0 {
		t.Errorf("fired %d rules at 9 matches against a threshold of 10, want 0", fired)
	}
	if events := h.history(t, integrationbus.AlertEventFilter{}); len(events) != 0 {
		t.Errorf("history has %d events before any breach, want 0", len(events))
	}

	// Over the threshold: fires once and notifies.
	counter.n = 25
	if fired, err = h.bus.EvaluateThresholds(ctx, counter); err != nil {
		t.Fatalf("evaluate over threshold: %v", err)
	}
	if fired != 1 {
		t.Fatalf("fired %d rules at 25 matches, want 1", fired)
	}

	events := h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events after a breach, want 1", len(events))
	}
	first := events[0]
	if first.MatchCount != 25 {
		t.Errorf("match_count = %d, want 25", first.MatchCount)
	}
	if h.provider.count() != 1 {
		t.Errorf("delivered %d notifications, want 1", h.provider.count())
	}

	// Still over on the next pass: the same alert, no second page.
	if _, err := h.bus.EvaluateThresholds(ctx, counter); err != nil {
		t.Fatalf("evaluate again: %v", err)
	}
	if events = h.history(t, integrationbus.AlertEventFilter{}); len(events) != 1 {
		t.Fatalf("history has %d events while still breaching, want 1", len(events))
	}
	if h.provider.count() != 1 {
		t.Errorf("delivered %d notifications while still breaching, want 1", h.provider.count())
	}

	// Back under: the open alert resolves.
	counter.n = 0
	if _, err := h.bus.EvaluateThresholds(ctx, counter); err != nil {
		t.Fatalf("evaluate back under: %v", err)
	}

	resolved, err := h.bus.QueryAlertEvent(ctx, first.ID)
	if err != nil {
		t.Fatalf("query event: %v", err)
	}
	if resolved.State != "resolved" {
		t.Errorf("state = %q, want resolved once back under the threshold", resolved.State)
	}
	if resolved.ResolvedAt == nil {
		t.Errorf("resolved_at is nil on a resolved alert")
	}

	// Breaching again opens a new alert instead of reviving the resolved one.
	counter.n = 30
	if _, err := h.bus.EvaluateThresholds(ctx, counter); err != nil {
		t.Fatalf("evaluate second breach: %v", err)
	}

	events = h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 2 {
		t.Fatalf("history has %d events after a second breach, want 2", len(events))
	}
	if events[0].ID == first.ID {
		t.Errorf("the second breach reused the resolved alert %s", first.ID)
	}
}

// Acknowledging owns an alert without closing it: no more notifications, but the
// dedup key stays taken so the alert is not duplicated either.
func Test_Alerting_Integration_AcknowledgeStopsNotifying(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	// A one second dedup window, so the next firing would otherwise notify.
	h.rule(t, integrationbus.NewAlertRule{Level: "ERROR", DedupWindowSeconds: 1})

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{payload("ERROR", "first")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	events := h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events, want 1", len(events))
	}

	acked, err := h.bus.AcknowledgeAlert(ctx, events[0].ID, h.fixture.UserID)
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if acked.State != "acknowledged" {
		t.Errorf("state = %q, want acknowledged", acked.State)
	}
	if acked.AcknowledgedBy == nil || *acked.AcknowledgedBy != h.fixture.UserID {
		t.Errorf("acknowledged_by = %v, want %s", acked.AcknowledgedBy, h.fixture.UserID)
	}

	before := h.provider.count()
	time.Sleep(1100 * time.Millisecond) // past the dedup window

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{payload("ERROR", "second")}); err != nil {
		t.Fatalf("fire after ack: %v", err)
	}

	if got := h.provider.count(); got != before {
		t.Errorf("delivered %d notifications after acknowledging, want %d", got, before)
	}
	if events = h.history(t, integrationbus.AlertEventFilter{}); len(events) != 1 {
		t.Errorf("history has %d events, want the acknowledged alert to absorb the firing", len(events))
	}
}

// History is what the alerts page reads, so each filter has to narrow correctly
// against real rows.
func Test_Alerting_Integration_HistoryFilters(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	second := uuid.New()
	if _, err := h.db.DB.Exec(`
		INSERT INTO projects (id, org_id, name, date_created, date_updated)
		VALUES ($1, $2, 'second', NOW(), NOW())`, second, h.fixture.OrgID); err != nil {
		t.Fatalf("second project: %v", err)
	}

	ruleA := h.rule(t, integrationbus.NewAlertRule{Name: "a", Level: "ERROR"})

	// A second rule on the other project, with its own connection there.
	connB, err := h.bus.Create(ctx, h.fixture.UserID, integrationbus.NewIntegration{
		OrgID:       h.fixture.OrgID,
		ProjectID:   second,
		ProviderID:  "slack",
		Name:        "secondary",
		Credentials: map[string]string{"webhookUrl": "https://hooks.example.test/y"},
	})
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}

	ruleB, err := h.bus.CreateRule(ctx, integrationbus.NewAlertRule{
		OrgID: h.fixture.OrgID, ProjectID: second, ConnectionID: connB.ID,
		UserID: h.fixture.UserID, Name: "b", Level: "WARN", IsActive: true,
	})
	if err != nil {
		t.Fatalf("second rule: %v", err)
	}

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{payload("ERROR", "on a")}); err != nil {
		t.Fatalf("fire a: %v", err)
	}
	if err := h.bus.FireAlerts(ctx, second, []integrationbus.AlertPayload{payload("WARN", "on b")}); err != nil {
		t.Fatalf("fire b: %v", err)
	}

	all := h.history(t, integrationbus.AlertEventFilter{})
	if len(all) != 2 {
		t.Fatalf("unfiltered history has %d events, want 2", len(all))
	}

	byProject := h.history(t, integrationbus.AlertEventFilter{ProjectID: &second})
	if len(byProject) != 1 || byProject[0].RuleID != ruleB.ID {
		t.Errorf("project filter returned %d events, want only rule b's", len(byProject))
	}

	byRule := h.history(t, integrationbus.AlertEventFilter{RuleID: &ruleA.ID})
	if len(byRule) != 1 || byRule[0].RuleID != ruleA.ID {
		t.Errorf("rule filter returned %d events, want only rule a's", len(byRule))
	}

	firing := "firing"
	if got := h.history(t, integrationbus.AlertEventFilter{State: &firing}); len(got) != 2 {
		t.Errorf("state=firing returned %d events, want 2", len(got))
	}

	if _, err := h.bus.ResolveAlert(ctx, all[0].ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := h.history(t, integrationbus.AlertEventFilter{State: &firing}); len(got) != 1 {
		t.Errorf("state=firing returned %d events after resolving one, want 1", len(got))
	}

	future := time.Now().Add(time.Hour)
	if got := h.history(t, integrationbus.AlertEventFilter{Since: &future}); len(got) != 0 {
		t.Errorf("since=future returned %d events, want 0", len(got))
	}

	past := time.Now().Add(-time.Hour)
	if got := h.history(t, integrationbus.AlertEventFilter{Since: &past}); len(got) != 2 {
		t.Errorf("since=an hour ago returned %d events, want 2", len(got))
	}

	if got := h.history(t, integrationbus.AlertEventFilter{Limit: 1}); len(got) != 1 {
		t.Errorf("limit=1 returned %d events, want 1", len(got))
	}

	// An event from another org must never appear, however the filter is set.
	other := h.db.SeedFixture(t, "free")
	if got := h.history(t, integrationbus.AlertEventFilter{OrgID: other.OrgID}); len(got) != 0 {
		t.Errorf("another org's history returned %d events, want 0", len(got))
	}
}

// A delivery failure must not mark the alert as notified, or the dedup window
// would silence the retry and the page would never arrive.
func Test_Alerting_Integration_FailedDeliveryRetries(t *testing.T) {
	h := newAlertHarness(t)
	ctx := context.Background()

	h.provider.err = errors.New("webhook down")
	h.rule(t, integrationbus.NewAlertRule{Level: "ERROR", DedupWindowSeconds: 3600})

	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{payload("ERROR", "first")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	events := h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events, want 1", len(events))
	}
	if events[0].LastNotifiedAt != nil {
		t.Fatalf("last_notified_at is set even though delivery failed")
	}

	// With the provider healthy again, the next firing delivers despite the hour
	// long dedup window, because nothing was ever notified.
	h.provider.err = nil
	if err := h.bus.FireAlerts(ctx, h.fixture.ProjectID, []integrationbus.AlertPayload{payload("ERROR", "second")}); err != nil {
		t.Fatalf("fire again: %v", err)
	}

	if got := h.provider.count(); got != 1 {
		t.Errorf("delivered %d notifications, want 1: a failed delivery must be retried", got)
	}

	events = h.history(t, integrationbus.AlertEventFilter{})
	if len(events) != 1 {
		t.Fatalf("history has %d events, want the retry folded into the open alert", len(events))
	}
	if events[0].LastNotifiedAt == nil {
		t.Errorf("last_notified_at is nil after a successful retry")
	}
}
