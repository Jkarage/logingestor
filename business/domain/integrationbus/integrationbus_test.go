package integrationbus_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// memStorer is an in-memory integrationbus.Storer for tests.
type memStorer struct {
	conns    map[uuid.UUID]integrationbus.Integration
	rules    map[uuid.UUID]integrationbus.AlertRule
	events   map[uuid.UUID]integrationbus.AlertEvent
	windows  []integrationbus.MaintenanceWindow
	notified int
}

func newMemStorer() *memStorer {
	return &memStorer{
		conns:  make(map[uuid.UUID]integrationbus.Integration),
		rules:  make(map[uuid.UUID]integrationbus.AlertRule),
		events: make(map[uuid.UUID]integrationbus.AlertEvent),
	}
}

func (m *memStorer) Create(_ context.Context, i integrationbus.Integration) error {
	m.conns[i.ID] = i
	return nil
}
func (m *memStorer) Update(_ context.Context, i integrationbus.Integration) error {
	m.conns[i.ID] = i
	return nil
}
func (m *memStorer) Delete(_ context.Context, i integrationbus.Integration) error {
	delete(m.conns, i.ID)
	return nil
}
func (m *memStorer) QueryByID(_ context.Context, id uuid.UUID) (integrationbus.Integration, error) {
	i, ok := m.conns[id]
	if !ok {
		return integrationbus.Integration{}, integrationbus.ErrNotFound
	}
	return i, nil
}
func (m *memStorer) QueryByOrg(_ context.Context, orgID uuid.UUID) ([]integrationbus.Integration, error) {
	var out []integrationbus.Integration
	for _, i := range m.conns {
		if i.OrgID == orgID {
			out = append(out, i)
		}
	}
	return out, nil
}
func (m *memStorer) QueryByProject(_ context.Context, projectID uuid.UUID) ([]integrationbus.Integration, error) {
	var out []integrationbus.Integration
	for _, i := range m.conns {
		if i.ProjectID == projectID {
			out = append(out, i)
		}
	}
	return out, nil
}
func (m *memStorer) QueryProviders(context.Context) ([]integrationbus.Provider, error) {
	return nil, nil
}
func (m *memStorer) CreateRule(_ context.Context, r integrationbus.AlertRule) error {
	m.rules[r.ID] = r
	return nil
}
func (m *memStorer) UpdateRule(_ context.Context, r integrationbus.AlertRule) error {
	m.rules[r.ID] = r
	return nil
}
func (m *memStorer) DeleteRule(_ context.Context, id uuid.UUID) error {
	delete(m.rules, id)
	return nil
}
func (m *memStorer) QueryRuleByID(_ context.Context, id uuid.UUID) (integrationbus.AlertRule, error) {
	r, ok := m.rules[id]
	if !ok {
		return integrationbus.AlertRule{}, integrationbus.ErrRuleNotFound
	}
	return r, nil
}
func (m *memStorer) QueryRulesByProject(_ context.Context, projectID uuid.UUID) ([]integrationbus.AlertRule, error) {
	var out []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.ProjectID == projectID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memStorer) QueryRulesByOrg(_ context.Context, orgID uuid.UUID) ([]integrationbus.AlertRule, error) {
	var out []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memStorer) DisableRulesByConnection(_ context.Context, connID uuid.UUID) error {
	for id, r := range m.rules {
		if r.ConnectionID == connID {
			r.IsActive = false
			m.rules[id] = r
		}
	}
	return nil
}
func (m *memStorer) QueryMatchingRules(_ context.Context, projectID uuid.UUID, levels []string) ([]integrationbus.AlertRule, error) {
	lv := map[string]bool{}
	for _, l := range levels {
		lv[l] = true
	}
	var out []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.ProjectID == projectID && r.IsActive && lv[r.Level] {
			out = append(out, r)
		}
	}
	return out, nil
}

func newTestBus(st integrationbus.Storer) *integrationbus.Business {
	lg := logger.New(discard{}, logger.LevelError, "TEST", nil)
	return integrationbus.NewBusiness(lg, st, map[string]integrationbus.Caller{"slack": nopCaller{}})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type nopCaller struct{}

func (nopCaller) Send(context.Context, map[string]string, integrationbus.AlertPayload) error {
	return nil
}

// Test_CreateRule_ConnectionMustBeInSameProject covers acceptance criterion 2.
func Test_CreateRule_ConnectionMustBeInSameProject(t *testing.T) {
	st := newMemStorer()
	bus := newTestBus(st)
	ctx := context.Background()

	orgID := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()

	connA := integrationbus.Integration{ID: uuid.New(), OrgID: orgID, ProjectID: projectA, ProviderID: "slack", Enabled: true}
	st.conns[connA.ID] = connA

	// Rule in project A pointing at project A's connection -> ok.
	okRule, err := bus.CreateRule(ctx, integrationbus.NewAlertRule{
		OrgID: orgID, ProjectID: projectA, ConnectionID: connA.ID, UserID: uuid.New(), Name: "r", Level: "ERROR", IsActive: true,
	})
	if err != nil {
		t.Fatalf("same-project rule should be allowed: %v", err)
	}
	if okRule.ProjectID != projectA {
		t.Errorf("rule project = %s, want %s", okRule.ProjectID, projectA)
	}
	if okRule.UserID == nil {
		t.Error("rule should record its owner")
	}

	// Rule in project B pointing at project A's connection -> rejected.
	_, err = bus.CreateRule(ctx, integrationbus.NewAlertRule{
		OrgID: orgID, ProjectID: projectB, ConnectionID: connA.ID, UserID: uuid.New(), Name: "r2", Level: "ERROR", IsActive: true,
	})
	if err == nil {
		t.Fatal("cross-project connection should be rejected")
	}
}

// Test_Disable_SuspendsRules covers acceptance criterion 6.
func Test_Disable_SuspendsRules(t *testing.T) {
	st := newMemStorer()
	bus := newTestBus(st)
	ctx := context.Background()

	orgID, projectID := uuid.New(), uuid.New()
	conn := integrationbus.Integration{ID: uuid.New(), OrgID: orgID, ProjectID: projectID, ProviderID: "slack", Enabled: true}
	st.conns[conn.ID] = conn

	rule, err := bus.CreateRule(ctx, integrationbus.NewAlertRule{
		OrgID: orgID, ProjectID: projectID, ConnectionID: conn.ID, UserID: uuid.New(), Name: "r", Level: "WARN", IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := bus.Disable(ctx, uuid.New(), conn); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if st.rules[rule.ID].IsActive {
		t.Error("rule bound to disabled connection should be suspended")
	}
	if st.conns[conn.ID].Enabled {
		t.Error("connection should be disabled")
	}
}

// =============================================================================
// Alerting evaluation. The event store is implemented for real rather than
// stubbed, because dedup and suppression are exactly the behaviour under test.

func (m *memStorer) QueryActiveRules(_ context.Context, projectID uuid.UUID) ([]integrationbus.AlertRule, error) {
	var out []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.ProjectID == projectID && r.IsActive {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStorer) QueryThresholdRules(context.Context) ([]integrationbus.AlertRule, error) {
	var out []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.IsActive && r.Condition.Type == integrationbus.ConditionThreshold {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStorer) QueryOpenEvent(_ context.Context, ruleID uuid.UUID, key string) (integrationbus.AlertEvent, error) {
	for _, e := range m.events {
		if e.RuleID == ruleID && e.DedupKey == key && e.State != integrationbus.AlertStateResolved {
			return e, nil
		}
	}
	return integrationbus.AlertEvent{}, integrationbus.ErrEventNotFound
}

// RecordFiring mirrors the upsert: one open event per (rule, key), a repeat
// accumulating onto it.
func (m *memStorer) RecordFiring(_ context.Context, e integrationbus.AlertEvent) (integrationbus.AlertEvent, error) {
	for id, ex := range m.events {
		if ex.RuleID == e.RuleID && ex.DedupKey == e.DedupKey && ex.State != integrationbus.AlertStateResolved {
			ex.LastSeenAt = e.LastSeenAt
			ex.MatchCount += e.MatchCount
			ex.Summary = e.Summary
			ex.Level = e.Level
			m.events[id] = ex
			return ex, nil
		}
	}
	e.ID = uuid.New()
	e.State = integrationbus.AlertStateFiring
	e.FirstSeenAt = e.LastSeenAt
	m.events[e.ID] = e
	return e, nil
}

func (m *memStorer) MarkNotified(_ context.Context, id uuid.UUID, at time.Time) error {
	e := m.events[id]
	t := at
	e.LastNotifiedAt = &t
	m.events[id] = e
	m.notified++
	return nil
}

func (m *memStorer) AcknowledgeEvent(_ context.Context, id, userID uuid.UUID, at time.Time) error {
	e := m.events[id]
	if e.State != integrationbus.AlertStateFiring {
		return nil
	}
	e.State = integrationbus.AlertStateAcknowledged
	e.AcknowledgedAt = &at
	e.AcknowledgedBy = &userID
	m.events[id] = e
	return nil
}

func (m *memStorer) ResolveEvent(_ context.Context, id uuid.UUID, at time.Time) error {
	e := m.events[id]
	e.State = integrationbus.AlertStateResolved
	e.ResolvedAt = &at
	m.events[id] = e
	return nil
}

func (m *memStorer) QueryEventByID(_ context.Context, id uuid.UUID) (integrationbus.AlertEvent, error) {
	e, ok := m.events[id]
	if !ok {
		return integrationbus.AlertEvent{}, integrationbus.ErrEventNotFound
	}
	return e, nil
}

func (m *memStorer) QueryEvents(_ context.Context, f integrationbus.AlertEventFilter) ([]integrationbus.AlertEvent, error) {
	var out []integrationbus.AlertEvent
	for _, e := range m.events {
		if e.OrgID == f.OrgID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStorer) CreateMaintenance(_ context.Context, w integrationbus.MaintenanceWindow) error {
	m.windows = append(m.windows, w)
	return nil
}

func (m *memStorer) DeleteMaintenance(context.Context, uuid.UUID) error { return nil }

func (m *memStorer) QueryMaintenanceByOrg(context.Context, uuid.UUID) ([]integrationbus.MaintenanceWindow, error) {
	return m.windows, nil
}

func (m *memStorer) QueryActiveMaintenance(_ context.Context, orgID uuid.UUID, at time.Time) ([]integrationbus.MaintenanceWindow, error) {
	var out []integrationbus.MaintenanceWindow
	for _, w := range m.windows {
		if w.OrgID == orgID && !at.Before(w.StartsAt) && at.Before(w.EndsAt) {
			out = append(out, w)
		}
	}
	return out, nil
}

// countingCounter answers threshold evaluations with a fixed number.
type countingCounter struct{ n int }

func (c countingCounter) CountMatching(context.Context, uuid.UUID, integrationbus.Query, time.Time, time.Time) (int, error) {
	return c.n, nil
}

// alertFixture builds a bus with one active rule.
func alertFixture(t *testing.T, cond integrationbus.Condition) (*integrationbus.Business, *memStorer, integrationbus.AlertRule) {
	t.Helper()

	st := newMemStorer()
	orgID, projectID, connID := uuid.New(), uuid.New(), uuid.New()

	st.conns[connID] = integrationbus.Integration{
		ID: connID, OrgID: orgID, ProjectID: projectID,
		ProviderID: "slack", Enabled: true, Credentials: map[string]string{},
	}

	rule := integrationbus.AlertRule{
		ID: uuid.New(), OrgID: orgID, ProjectID: projectID, ConnectionID: connID,
		Name: "rule", Level: "ERROR", Condition: cond, IsActive: true,
		DedupWindowSeconds: 300,
	}
	st.rules[rule.ID] = rule

	return newTestBus(st), st, rule
}

func errPayload(msg string) integrationbus.AlertPayload {
	return integrationbus.AlertPayload{Level: "ERROR", Message: msg, Source: "api", Timestamp: time.Now()}
}

func Test_FireAlerts_LevelCondition(t *testing.T) {
	bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))

	if err := bus.FireAlerts(context.Background(), rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	if st.notified != 1 {
		t.Fatalf("notified %d times, want 1", st.notified)
	}
	if len(st.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(st.events))
	}
	for _, e := range st.events {
		if e.State != integrationbus.AlertStateFiring {
			t.Errorf("state = %q, want firing", e.State)
		}
	}
}

// A burst must become one notification, and the counts must still add up.
func Test_FireAlerts_DedupCollapsesRepeats(t *testing.T) {
	bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
			t.Fatalf("fire %d: %v", i, err)
		}
	}

	if st.notified != 1 {
		t.Errorf("notified %d times, want 1 — the dedup window should hold", st.notified)
	}
	if len(st.events) != 1 {
		t.Errorf("recorded %d events, want them folded into 1", len(st.events))
	}
	for _, e := range st.events {
		if e.MatchCount != 5 {
			t.Errorf("match count = %d, want 5 accumulated", e.MatchCount)
		}
	}
}

// One batch containing many matches is one alert that reports how many.
func Test_FireAlerts_BatchOfManyIsOneAlert(t *testing.T) {
	bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))

	batch := []integrationbus.AlertPayload{errPayload("a"), errPayload("b"), errPayload("c")}
	if err := bus.FireAlerts(context.Background(), rule.ProjectID, batch); err != nil {
		t.Fatalf("fire: %v", err)
	}

	if st.notified != 1 {
		t.Errorf("notified %d times, want 1", st.notified)
	}
	for _, e := range st.events {
		if e.MatchCount != 3 {
			t.Errorf("match count = %d, want 3", e.MatchCount)
		}
		if !strings.Contains(e.Summary, "+2 more") {
			t.Errorf("summary %q should say how many more matched", e.Summary)
		}
	}
}

func Test_FireAlerts_MatchCondition(t *testing.T) {
	cond := integrationbus.Condition{
		Type:  integrationbus.ConditionMatch,
		Query: &integrationbus.Query{Contains: "timeout"},
	}
	bus, st, rule := alertFixture(t, cond)
	ctx := context.Background()

	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("disk full")}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	if st.notified != 0 {
		t.Fatalf("a non-matching log must not alert")
	}

	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("upstream TIMEOUT")}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	if st.notified != 1 {
		t.Errorf("notified %d times, want 1", st.notified)
	}
}

func Test_FireAlerts_Suppression(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(*memStorer, *integrationbus.AlertRule)
	}{
		{
			name: "snoozed rule",
			arrange: func(st *memStorer, r *integrationbus.AlertRule) {
				until := time.Now().Add(time.Hour)
				r.SnoozeUntil = &until
				st.rules[r.ID] = *r
			},
		},
		{
			name: "maintenance window covering the org",
			arrange: func(st *memStorer, r *integrationbus.AlertRule) {
				st.windows = append(st.windows, integrationbus.MaintenanceWindow{
					OrgID: r.OrgID, StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour),
				})
			},
		},
		{
			name: "inactive rule",
			arrange: func(st *memStorer, r *integrationbus.AlertRule) {
				r.IsActive = false
				st.rules[r.ID] = *r
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))
			c.arrange(st, &rule)

			if err := bus.FireAlerts(context.Background(), rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
				t.Fatalf("fire: %v", err)
			}

			if st.notified != 0 {
				t.Errorf("notified %d times, want 0 — %s should suppress", st.notified, c.name)
			}
		})
	}
}

// Acknowledging stops the paging without resolving the alert.
func Test_FireAlerts_AcknowledgedStopsNotifying(t *testing.T) {
	bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))
	ctx := context.Background()

	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	var eventID uuid.UUID
	for id := range st.events {
		eventID = id
	}
	if _, err := bus.AcknowledgeAlert(ctx, eventID, uuid.New()); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// Past the dedup window, so only the acknowledgement can be holding it back.
	e := st.events[eventID]
	old := time.Now().Add(-time.Hour)
	e.LastNotifiedAt = &old
	st.events[eventID] = e

	before := st.notified
	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom again")}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	if st.notified != before {
		t.Error("an acknowledged alert must not re-notify")
	}
	if st.events[eventID].State != integrationbus.AlertStateAcknowledged {
		t.Error("acknowledgement should persist")
	}
}

// A persistent problem must page again once the window has elapsed.
func Test_FireAlerts_RenotifiesAfterWindow(t *testing.T) {
	bus, st, rule := alertFixture(t, integrationbus.LevelCondition("ERROR"))
	ctx := context.Background()

	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	for id, e := range st.events {
		old := time.Now().Add(-2 * time.Hour)
		e.LastNotifiedAt = &old
		st.events[id] = e
	}

	if err := bus.FireAlerts(ctx, rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	if st.notified != 2 {
		t.Errorf("notified %d times, want 2 once the window elapsed", st.notified)
	}
}

// A threshold rule is a statement about a window, so a single batch must not
// decide it.
func Test_FireAlerts_SkipsThresholdRules(t *testing.T) {
	cond := integrationbus.Condition{
		Type: integrationbus.ConditionThreshold, Query: &integrationbus.Query{Levels: []string{"ERROR"}},
		WindowSeconds: 300, Count: 10, Comparator: integrationbus.ComparatorGTE,
	}
	bus, st, rule := alertFixture(t, cond)

	if err := bus.FireAlerts(context.Background(), rule.ProjectID, []integrationbus.AlertPayload{errPayload("boom")}); err != nil {
		t.Fatalf("fire: %v", err)
	}

	if st.notified != 0 || len(st.events) != 0 {
		t.Error("FireAlerts must leave threshold rules to the evaluator")
	}
}

func Test_EvaluateThresholds(t *testing.T) {
	cond := integrationbus.Condition{
		Type: integrationbus.ConditionThreshold, Query: &integrationbus.Query{Levels: []string{"ERROR"}},
		WindowSeconds: 300, Count: 10, Comparator: integrationbus.ComparatorGTE,
	}

	t.Run("under the threshold does not fire", func(t *testing.T) {
		bus, st, _ := alertFixture(t, cond)
		fired, err := bus.EvaluateThresholds(context.Background(), countingCounter{n: 9})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if fired != 0 || st.notified != 0 {
			t.Errorf("fired=%d notified=%d, want 0/0", fired, st.notified)
		}
	})

	t.Run("at the threshold fires once", func(t *testing.T) {
		bus, st, _ := alertFixture(t, cond)
		fired, err := bus.EvaluateThresholds(context.Background(), countingCounter{n: 10})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if fired != 1 || st.notified != 1 {
			t.Errorf("fired=%d notified=%d, want 1/1", fired, st.notified)
		}
	})

	// Still breaching on the next pass must not page again inside the window.
	t.Run("staying over does not re-page", func(t *testing.T) {
		bus, st, _ := alertFixture(t, cond)
		ctx := context.Background()
		for i := 0; i < 4; i++ {
			if _, err := bus.EvaluateThresholds(ctx, countingCounter{n: 50}); err != nil {
				t.Fatalf("evaluate %d: %v", i, err)
			}
		}
		if st.notified != 1 {
			t.Errorf("notified %d times, want 1", st.notified)
		}
	})

	// Dropping back under closes the alert so the next breach reads as new.
	t.Run("dropping under resolves the alert", func(t *testing.T) {
		bus, st, _ := alertFixture(t, cond)
		ctx := context.Background()

		if _, err := bus.EvaluateThresholds(ctx, countingCounter{n: 20}); err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if _, err := bus.EvaluateThresholds(ctx, countingCounter{n: 1}); err != nil {
			t.Fatalf("evaluate: %v", err)
		}

		for _, e := range st.events {
			if e.State != integrationbus.AlertStateResolved {
				t.Errorf("state = %q, want resolved", e.State)
			}
		}

		if _, err := bus.EvaluateThresholds(ctx, countingCounter{n: 20}); err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if st.notified != 2 {
			t.Errorf("notified %d times, want 2 — a breach after resolve is a new alert", st.notified)
		}
	})
}
