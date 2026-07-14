package integrationbus_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// memStorer is an in-memory integrationbus.Storer for tests.
type memStorer struct {
	conns map[uuid.UUID]integrationbus.Integration
	rules map[uuid.UUID]integrationbus.AlertRule
}

func newMemStorer() *memStorer {
	return &memStorer{
		conns: make(map[uuid.UUID]integrationbus.Integration),
		rules: make(map[uuid.UUID]integrationbus.AlertRule),
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
