package integrationapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/foundation/logger"
)

// --- in-memory integrationbus.Storer -----------------------------------------

type memStorer struct {
	conns map[uuid.UUID]integrationbus.Integration
	rules map[uuid.UUID]integrationbus.AlertRule
}

func newMem() *memStorer {
	return &memStorer{conns: map[uuid.UUID]integrationbus.Integration{}, rules: map[uuid.UUID]integrationbus.AlertRule{}}
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
	var o []integrationbus.Integration
	for _, i := range m.conns {
		if i.OrgID == orgID {
			o = append(o, i)
		}
	}
	return o, nil
}
func (m *memStorer) QueryByProject(_ context.Context, pid uuid.UUID) ([]integrationbus.Integration, error) {
	var o []integrationbus.Integration
	for _, i := range m.conns {
		if i.ProjectID == pid {
			o = append(o, i)
		}
	}
	return o, nil
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
func (m *memStorer) QueryRulesByProject(_ context.Context, pid uuid.UUID) ([]integrationbus.AlertRule, error) {
	var o []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.ProjectID == pid {
			o = append(o, r)
		}
	}
	return o, nil
}
func (m *memStorer) QueryRulesByOrg(_ context.Context, orgID uuid.UUID) ([]integrationbus.AlertRule, error) {
	var o []integrationbus.AlertRule
	for _, r := range m.rules {
		if r.OrgID == orgID {
			o = append(o, r)
		}
	}
	return o, nil
}
func (m *memStorer) DisableRulesByConnection(context.Context, uuid.UUID) error { return nil }
func (m *memStorer) QueryMatchingRules(context.Context, uuid.UUID, []string) ([]integrationbus.AlertRule, error) {
	return nil, nil
}

// --- fakes for the app's other deps ------------------------------------------

type fakeProjectBus struct {
	projectbus.ExtBusiness
	project projectbus.Project
	err     error
}

func (f fakeProjectBus) QueryByID(context.Context, uuid.UUID) (projectbus.Project, error) {
	return f.project, f.err
}

type fakeUserBus struct {
	userbus.ExtBusiness
	user userbus.User
}

func (f fakeUserBus) QueryByID(context.Context, uuid.UUID) (userbus.User, error) { return f.user, nil }

type noopAudit struct{ auditbus.ExtBusiness }

func (noopAudit) Create(context.Context, auditbus.NewAudit) (auditbus.Audit, error) {
	return auditbus.Audit{}, nil
}

func newTestApp(st integrationbus.Storer, pb projectbus.ExtBusiness, ub userbus.ExtBusiness) *app {
	lg := logger.New(discard{}, logger.LevelError, "TEST", nil)
	bus := integrationbus.NewBusiness(lg, st, map[string]integrationbus.Caller{"slack": nopCaller{}})
	return newApp(bus, pb, ub, noopAudit{})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type nopCaller struct{}

func (nopCaller) Send(context.Context, map[string]string, integrationbus.AlertPayload) error {
	return nil
}

func httpBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// --- tests -------------------------------------------------------------------

// Test_list_ScopedToProject: a connection is only visible under its own project
// (acceptance criterion 1 & 5).
func Test_list_ScopedToProject(t *testing.T) {
	st := newMem()
	orgID, projectA, projectB := uuid.New(), uuid.New(), uuid.New()
	st.conns[uuid.New()] = integrationbus.Integration{ID: uuid.New(), OrgID: orgID, ProjectID: projectA, ProviderID: "slack", Name: "A-slack", Enabled: true}
	st.conns[uuid.New()] = integrationbus.Integration{ID: uuid.New(), OrgID: orgID, ProjectID: projectB, ProviderID: "slack", Name: "B-slack", Enabled: true}

	a := newTestApp(st, fakeProjectBus{}, fakeUserBus{})

	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("project_id", projectA.String())

	resp := a.list(context.Background(), r)
	cr, ok := resp.(connectionsResponse)
	if !ok {
		t.Fatalf("expected connectionsResponse, got %T", resp)
	}
	if len(cr.Connections) != 1 || cr.Connections[0].Name != "A-slack" {
		t.Fatalf("expected only project A's connection, got %+v", cr.Connections)
	}
	if cr.Connections[0].ProjectID != projectA.String() {
		t.Errorf("connection projectId = %s, want %s", cr.Connections[0].ProjectID, projectA)
	}
}

// Test_listRules_OwnerDisplay: rules are team-visible and annotated with owner
// display info (acceptance criterion 3).
func Test_listRules_OwnerDisplay(t *testing.T) {
	st := newMem()
	orgID, projectID, ownerID := uuid.New(), uuid.New(), uuid.New()
	st.rules[uuid.New()] = integrationbus.AlertRule{
		ID: uuid.New(), OrgID: orgID, ProjectID: projectID, ConnectionID: uuid.New(),
		UserID: &ownerID, Name: "r", Level: "ERROR", IsActive: true,
	}

	addr, _ := mail.ParseAddress("alice@example.com")
	ub := fakeUserBus{user: userbus.User{ID: ownerID, Name: name.MustParse("Alice"), Email: *addr}}

	a := newTestApp(st, fakeProjectBus{}, ub)

	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("project_id", projectID.String())

	resp := a.listRules(context.Background(), r)
	data, _, _ := resp.Encode()
	var out struct {
		Rules []AppAlertRule `json:"rules"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(out.Rules))
	}
	got := out.Rules[0]
	if got.UserID == nil || *got.UserID != ownerID.String() {
		t.Errorf("userId = %v, want %s", got.UserID, ownerID)
	}
	if got.OwnerName != "Alice" || got.OwnerEmail != "alice@example.com" {
		t.Errorf("owner display = %q/%q, want Alice/alice@example.com", got.OwnerName, got.OwnerEmail)
	}
}

// Test_updateRule_CrossProject_404: a rule cannot be reached from another
// project's path (acceptance criterion 5).
func Test_updateRule_CrossProject_404(t *testing.T) {
	st := newMem()
	orgID, projectA, projectB := uuid.New(), uuid.New(), uuid.New()
	ruleID := uuid.New()
	st.rules[ruleID] = integrationbus.AlertRule{ID: ruleID, OrgID: orgID, ProjectID: projectA, Name: "r", Level: "WARN", IsActive: true}

	// projectBus says project B is valid and in the org (so the 404 comes from
	// the rule-not-in-project check, not the project check).
	pb := fakeProjectBus{project: projectbus.Project{ID: projectB, OrgID: orgID}}
	a := newTestApp(st, pb, fakeUserBus{})

	r := httptest.NewRequest("PUT", "/x", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("project_id", projectB.String())
	r.SetPathValue("rule_id", ruleID.String())
	r.Body = httpBody(`{"name":"hacked"}`)

	resp := a.updateRule(context.Background(), r)
	e, ok := resp.(*errs.Error)
	if !ok || e.Code != errs.NotFound {
		t.Fatalf("expected NotFound, got %T %v", resp, resp)
	}
}

// Test_listRulesByOrg_Aggregate: the org aggregate returns rules from every
// project in the org (annotated with projectId + owner) and never leaks rules
// from another org.
func Test_listRulesByOrg_Aggregate(t *testing.T) {
	st := newMem()
	orgID, otherOrg := uuid.New(), uuid.New()
	projectA, projectB := uuid.New(), uuid.New()
	ownerID := uuid.New()

	st.rules[uuid.New()] = integrationbus.AlertRule{ID: uuid.New(), OrgID: orgID, ProjectID: projectA, UserID: &ownerID, Name: "a", Level: "ERROR", IsActive: true}
	st.rules[uuid.New()] = integrationbus.AlertRule{ID: uuid.New(), OrgID: orgID, ProjectID: projectB, UserID: &ownerID, Name: "b", Level: "WARN", IsActive: true}
	st.rules[uuid.New()] = integrationbus.AlertRule{ID: uuid.New(), OrgID: otherOrg, ProjectID: uuid.New(), Name: "other", Level: "INFO", IsActive: true}

	addr, _ := mail.ParseAddress("bob@example.com")
	ub := fakeUserBus{user: userbus.User{ID: ownerID, Name: name.MustParse("Bob"), Email: *addr}}
	a := newTestApp(st, fakeProjectBus{}, ub)

	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("org_id", orgID.String())

	resp := a.listRulesByOrg(context.Background(), r)
	data, _, _ := resp.Encode()
	var out struct {
		Rules []AppAlertRule `json:"rules"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Rules) != 2 {
		t.Fatalf("expected 2 rules for the org (no cross-org leak), got %d", len(out.Rules))
	}
	projects := map[string]bool{}
	for _, rule := range out.Rules {
		if rule.ProjectID == "" {
			t.Error("aggregate rule missing projectId")
		}
		if rule.OwnerName != "Bob" {
			t.Errorf("ownerName = %q, want Bob", rule.OwnerName)
		}
		projects[rule.ProjectID] = true
	}
	if !projects[projectA.String()] || !projects[projectB.String()] {
		t.Errorf("expected rules from both projects, got %v", projects)
	}
}

// Test_create_ProjectNotInOrg_404: creating a connection under an org that does
// not own the project is rejected (acceptance criterion 5).
func Test_create_ProjectNotInOrg_404(t *testing.T) {
	st := newMem()
	orgID, otherOrg, projectID := uuid.New(), uuid.New(), uuid.New()
	pb := fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: otherOrg}}
	a := newTestApp(st, pb, fakeUserBus{})

	r := httptest.NewRequest("POST", "/x", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("project_id", projectID.String())
	r.Body = httpBody(`{"provider":"slack","name":"n","credentials":{"webhookUrl":"x"}}`)

	resp := a.create(context.Background(), r)
	e, ok := resp.(*errs.Error)
	if !ok || e.Code != errs.NotFound {
		t.Fatalf("expected NotFound, got %T %v", resp, resp)
	}
}
