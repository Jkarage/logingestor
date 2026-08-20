package integrationbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound             = errors.New("integration not found")
	ErrDuplicateName        = errors.New("integration name already exists for this provider in org")
	ErrUnknownProvider      = errors.New("unknown integration provider")
	ErrProviderRejected     = errors.New("provider rejected the request")
	ErrRuleNotFound         = errors.New("alert rule not found")
	ErrInvalidLevel         = errors.New("level must be one of DEBUG, INFO, WARN, ERROR")
	ErrConnectionBadOrg     = errors.New("connection does not belong to this org")
	ErrConnectionBadProject = errors.New("connection does not belong to this project")
)

// Storer declares the persistence behaviour this package needs.
type Storer interface {
	Create(ctx context.Context, i Integration) error
	Update(ctx context.Context, i Integration) error
	Delete(ctx context.Context, i Integration) error
	QueryByID(ctx context.Context, id uuid.UUID) (Integration, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Integration, error)
	QueryByProject(ctx context.Context, projectID uuid.UUID) ([]Integration, error)
	QueryProviders(ctx context.Context) ([]Provider, error)

	CreateRule(ctx context.Context, r AlertRule) error
	UpdateRule(ctx context.Context, r AlertRule) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	QueryRuleByID(ctx context.Context, id uuid.UUID) (AlertRule, error)
	QueryRulesByProject(ctx context.Context, projectID uuid.UUID) ([]AlertRule, error)
	QueryRulesByOrg(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error)
	DisableRulesByConnection(ctx context.Context, connectionID uuid.UUID) error
	QueryMatchingRules(ctx context.Context, projectID uuid.UUID, levels []string) ([]AlertRule, error)
	QueryActiveRules(ctx context.Context, projectID uuid.UUID) ([]AlertRule, error)
	QueryThresholdRules(ctx context.Context) ([]AlertRule, error)

	QueryOpenEvent(ctx context.Context, ruleID uuid.UUID, dedupKey string) (AlertEvent, error)
	RecordFiring(ctx context.Context, e AlertEvent) (AlertEvent, error)
	MarkNotified(ctx context.Context, eventID uuid.UUID, at time.Time) error
	AcknowledgeEvent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error
	ResolveEvent(ctx context.Context, eventID uuid.UUID, at time.Time) error
	QueryEventByID(ctx context.Context, id uuid.UUID) (AlertEvent, error)
	QueryEvents(ctx context.Context, f AlertEventFilter) ([]AlertEvent, error)

	CreateMaintenance(ctx context.Context, w MaintenanceWindow) error
	DeleteMaintenance(ctx context.Context, id uuid.UUID) error
	QueryMaintenanceByOrg(ctx context.Context, orgID uuid.UUID) ([]MaintenanceWindow, error)
	QueryActiveMaintenance(ctx context.Context, orgID uuid.UUID, at time.Time) ([]MaintenanceWindow, error)
}

// Business manages the set of APIs for the integration domain.
type Business struct {
	log     *logger.Logger
	storer  Storer
	callers map[string]Caller
}

// NewBusiness constructs an integration business API for use.
func NewBusiness(log *logger.Logger, storer Storer, callers map[string]Caller) *Business {
	return &Business{
		log:     log,
		storer:  storer,
		callers: callers,
	}
}

// QueryProviders returns all enabled integration provider definitions.
func (b *Business) QueryProviders(ctx context.Context) ([]Provider, error) {
	providers, err := b.storer.QueryProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("queryproviders: %w", err)
	}
	return providers, nil
}

// Create adds a new integration to the system.
func (b *Business) Create(ctx context.Context, actorID uuid.UUID, ni NewIntegration) (Integration, error) {
	if _, ok := b.callers[ni.ProviderID]; !ok {
		return Integration{}, fmt.Errorf("create: %w", ErrUnknownProvider)
	}

	now := time.Now()
	i := Integration{
		ID:          uuid.New(),
		OrgID:       ni.OrgID,
		ProjectID:   ni.ProjectID,
		ProviderID:  ni.ProviderID,
		Name:        ni.Name,
		Credentials: ni.Credentials,
		Enabled:     true,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.Create(ctx, i); err != nil {
		return Integration{}, fmt.Errorf("create: %w", err)
	}

	return i, nil
}

// Update modifies an existing integration.
func (b *Business) Update(ctx context.Context, actorID uuid.UUID, i Integration, ui UpdateIntegration) (Integration, error) {
	if ui.Name != nil {
		i.Name = *ui.Name
	}
	if ui.Credentials != nil {
		i.Credentials = ui.Credentials
	}
	if ui.Enabled != nil {
		i.Enabled = *ui.Enabled
	}
	i.DateUpdated = time.Now()

	if err := b.storer.Update(ctx, i); err != nil {
		return Integration{}, fmt.Errorf("update: %w", err)
	}

	return i, nil
}

// Delete removes an integration from the system.
func (b *Business) Delete(ctx context.Context, actorID uuid.UUID, i Integration) error {
	if err := b.storer.Delete(ctx, i); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Disable soft-deletes an integration (sets enabled=false) and suspends all its rules.
func (b *Business) Disable(ctx context.Context, actorID uuid.UUID, i Integration) error {
	disabled := false
	i.Enabled = disabled
	i.DateUpdated = time.Now()

	if err := b.storer.Update(ctx, i); err != nil {
		return fmt.Errorf("disable: update: %w", err)
	}

	if err := b.storer.DisableRulesByConnection(ctx, i.ID); err != nil {
		return fmt.Errorf("disable: suspend rules: %w", err)
	}

	return nil
}

// QueryByID returns the integration identified by id.
func (b *Business) QueryByID(ctx context.Context, id uuid.UUID) (Integration, error) {
	i, err := b.storer.QueryByID(ctx, id)
	if err != nil {
		return Integration{}, fmt.Errorf("querybyid: %w", err)
	}
	return i, nil
}

// QueryByOrg returns all integrations across an org's projects. Intended for
// read-only admin aggregate views; writes always go through a project.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Integration, error) {
	integrations, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querybyorg: %w", err)
	}
	return integrations, nil
}

// QueryByProject returns all integration connections owned by a project.
func (b *Business) QueryByProject(ctx context.Context, projectID uuid.UUID) ([]Integration, error) {
	integrations, err := b.storer.QueryByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("querybyproject: %w", err)
	}
	return integrations, nil
}

// Test sends a test alert through the integration to verify credentials work.
func (b *Business) Test(ctx context.Context, i Integration) error {
	caller, ok := b.callers[i.ProviderID]
	if !ok {
		return fmt.Errorf("test: %w", ErrUnknownProvider)
	}

	payload := AlertPayload{
		ProjectName: "Test Project",
		Level:       "INFO",
		Message:     "This is a test alert from streamlogia. Your integration is working correctly.",
		Source:      "logingestor/test",
		LogID:       "12345678-1234-1234-1234-12345678",
		Timestamp:   time.Now(),
	}

	if err := caller.Send(ctx, i.Credentials, payload); err != nil {
		return fmt.Errorf("test: send: %w: %w", ErrProviderRejected, err)
	}

	return nil
}

// =============================================================================
// Alert Rule business methods

// CreateRule adds a new alert rule for an org.
func (b *Business) CreateRule(ctx context.Context, nr NewAlertRule) (AlertRule, error) {
	if !ValidLevels[nr.Level] {
		return AlertRule{}, fmt.Errorf("createrule: %w", ErrInvalidLevel)
	}

	conn, err := b.storer.QueryByID(ctx, nr.ConnectionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return AlertRule{}, fmt.Errorf("createrule: %w", ErrNotFound)
		}
		return AlertRule{}, fmt.Errorf("createrule: %w", err)
	}

	// The connection must belong to the rule's project (which implies its org).
	if conn.ProjectID != nr.ProjectID {
		return AlertRule{}, fmt.Errorf("createrule: %w", ErrConnectionBadProject)
	}
	if conn.OrgID != nr.OrgID {
		return AlertRule{}, fmt.Errorf("createrule: %w", ErrConnectionBadOrg)
	}

	// An omitted condition means the rule fires at or above its level, which is
	// what every rule did before conditions existed.
	cond := nr.Condition
	if cond.Type == "" {
		cond = LevelCondition(nr.Level)
	}
	if err := cond.Validate(); err != nil {
		return AlertRule{}, fmt.Errorf("createrule: %w", err)
	}

	now := time.Now()
	userID := nr.UserID
	r := AlertRule{
		ID:                 uuid.New(),
		OrgID:              nr.OrgID,
		ProjectID:          nr.ProjectID,
		ConnectionID:       nr.ConnectionID,
		UserID:             &userID,
		Name:               nr.Name,
		Level:              nr.Level,
		Condition:          cond,
		DedupWindowSeconds: nr.DedupWindowSeconds,
		IsActive:           nr.IsActive,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := b.storer.CreateRule(ctx, r); err != nil {
		return AlertRule{}, fmt.Errorf("createrule: %w", err)
	}

	return r, nil
}

// UpdateRule modifies an existing alert rule.
func (b *Business) UpdateRule(ctx context.Context, r AlertRule, ur UpdateAlertRule) (AlertRule, error) {
	if ur.Name != nil {
		r.Name = *ur.Name
	}
	if ur.Level != nil {
		if !ValidLevels[*ur.Level] {
			return AlertRule{}, fmt.Errorf("updaterule: %w", ErrInvalidLevel)
		}
		r.Level = *ur.Level
	}
	if ur.ConnectionID != nil {
		// A rule may only route to a connection in its own project.
		conn, err := b.storer.QueryByID(ctx, *ur.ConnectionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return AlertRule{}, fmt.Errorf("updaterule: %w", ErrNotFound)
			}
			return AlertRule{}, fmt.Errorf("updaterule: %w", err)
		}
		if conn.ProjectID != r.ProjectID {
			return AlertRule{}, fmt.Errorf("updaterule: %w", ErrConnectionBadProject)
		}
		r.ConnectionID = *ur.ConnectionID
	}
	if ur.IsActive != nil {
		r.IsActive = *ur.IsActive
	}
	if ur.Condition != nil {
		if err := ur.Condition.Validate(); err != nil {
			return AlertRule{}, fmt.Errorf("updaterule: %w", err)
		}
		r.Condition = *ur.Condition
	}
	if ur.DedupWindowSeconds != nil {
		r.DedupWindowSeconds = *ur.DedupWindowSeconds
	}
	if ur.SnoozeUntil != nil {
		r.SnoozeUntil = *ur.SnoozeUntil
	}

	// A level change with no explicit condition keeps the two consistent: a
	// level rule must not keep firing on its old severity.
	if ur.Level != nil && ur.Condition == nil && r.Condition.Type == ConditionLevel {
		r.Condition = LevelCondition(r.Level)
	}

	r.UpdatedAt = time.Now()

	if err := b.storer.UpdateRule(ctx, r); err != nil {
		return AlertRule{}, fmt.Errorf("updaterule: %w", err)
	}

	return r, nil
}

// DeleteRule removes an alert rule.
func (b *Business) DeleteRule(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.DeleteRule(ctx, id); err != nil {
		return fmt.Errorf("deleterule: %w", err)
	}
	return nil
}

// QueryRuleByID returns the rule identified by id.
func (b *Business) QueryRuleByID(ctx context.Context, id uuid.UUID) (AlertRule, error) {
	r, err := b.storer.QueryRuleByID(ctx, id)
	if err != nil {
		return AlertRule{}, fmt.Errorf("queryrulebyid: %w", err)
	}
	return r, nil
}

// QueryRulesByProject returns all alert rules for a project (team-visible).
func (b *Business) QueryRulesByProject(ctx context.Context, projectID uuid.UUID) ([]AlertRule, error) {
	rules, err := b.storer.QueryRulesByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("queryrulesbyproject: %w", err)
	}
	return rules, nil
}

// QueryRulesByOrg returns all alert rules across an org's projects. Intended
// for read-only admin aggregate views; writes always go through a project.
func (b *Business) QueryRulesByOrg(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rules, err := b.storer.QueryRulesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("queryrulesbyorg: %w", err)
	}
	return rules, nil
}

// levelThreshold returns all rule levels that should fire for a given log level.
// A rule fires when its configured level <= the log's level in severity.
func levelThreshold(logLevel string) []string {
	order := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	for i, l := range order {
		if l == logLevel {
			return order[:i+1]
		}
	}
	return nil
}

// severityRank orders log levels; higher is more severe. Unknown levels rank
// below DEBUG so they never satisfy a rule.
func severityRank(level string) int {
	switch level {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN":
		return 2
	case "ERROR":
		return 3
	}
	return -1
}

// SendAlert decrypts credentials for connectionID and delivers payload to the provider.
func (b *Business) SendAlert(ctx context.Context, connectionID uuid.UUID, payload AlertPayload) error {
	integration, err := b.storer.QueryByID(ctx, connectionID)
	if err != nil {
		return fmt.Errorf("sendalert: querybyid: %w", err)
	}

	if !integration.Enabled {
		return nil
	}

	caller, ok := b.callers[integration.ProviderID]
	if !ok {
		return fmt.Errorf("sendalert: unknown provider %q", integration.ProviderID)
	}

	if err := caller.Send(ctx, integration.Credentials, payload); err != nil {
		return fmt.Errorf("sendalert: send: %w", err)
	}

	return nil
}

// FireAlerts evaluates a project's active rules against a batch of logs and
// delivers whatever survives suppression.
//
// Threshold rules are skipped here on purpose: they are a statement about a time
// window, which a single batch cannot answer. EvaluateThresholds owns those.
func (b *Business) FireAlerts(ctx context.Context, projectID uuid.UUID, payloads []AlertPayload) error {
	if len(payloads) == 0 {
		return nil
	}

	rules, err := b.storer.QueryActiveRules(ctx, projectID)
	if err != nil {
		return fmt.Errorf("firealerts: queryactiverules: %w", err)
	}
	if len(rules) == 0 {
		return nil
	}

	now := time.Now()

	// Maintenance is an org-level statement, so it is loaded once per batch
	// rather than per rule.
	windows, err := b.storer.QueryActiveMaintenance(ctx, rules[0].OrgID, now)
	if err != nil {
		b.log.Error(ctx, "firealerts: maintenance lookup", "orgID", rules[0].OrgID, "err", err)
	}

	for _, rule := range rules {
		if rule.Condition.NeedsEvaluator() {
			continue
		}

		matched, rep := matchPayloads(rule.Condition, payloads)
		if matched == 0 {
			continue
		}

		if err := b.raise(ctx, rule, *rep, matched, windows, now); err != nil {
			b.log.Error(ctx, "firealerts: raise", "ruleID", rule.ID, "err", err)
		}
	}

	return nil
}

// raise records a firing, decides whether it may be delivered, and delivers it.
//
// The event is recorded before the suppression decision so history stays complete
// even when nothing is sent — "it fired but I was not paged, why" is exactly the
// question dedup and maintenance windows make people ask.
func (b *Business) raise(ctx context.Context, rule AlertRule, rep AlertPayload, matched int, windows []MaintenanceWindow, now time.Time) error {
	dedupKey := rule.Condition.DedupKey(rep.Level)

	var prior *AlertEvent
	existing, err := b.storer.QueryOpenEvent(ctx, rule.ID, dedupKey)
	switch {
	case err == nil:
		prior = &existing
	case errors.Is(err, ErrEventNotFound):
	default:
		return fmt.Errorf("queryopenevent: %w", err)
	}

	summary := rep.Message
	if matched > 1 {
		summary = fmt.Sprintf("%s (+%d more matching logs)", summary, matched-1)
	}

	var sampleLogID *uuid.UUID
	if id, err := uuid.Parse(rep.LogID); err == nil {
		sampleLogID = &id
	}

	event, err := b.storer.RecordFiring(ctx, AlertEvent{
		RuleID: rule.ID, OrgID: rule.OrgID, ProjectID: rule.ProjectID,
		DedupKey: dedupKey, Summary: summary, Level: rep.Level,
		MatchCount: int64(matched), SampleLogID: sampleLogID, LastSeenAt: now,
	})
	if err != nil {
		return fmt.Errorf("recordfiring: %w", err)
	}

	ok, why := ShouldNotify(SuppressInput{
		Now:          now,
		RuleActive:   rule.IsActive,
		SnoozeUntil:  rule.SnoozeUntil,
		DedupWindow:  rule.DedupWindow(),
		ProjectID:    rule.ProjectID,
		Existing:     prior,
		Maintenances: windows,
	})
	if !ok {
		b.log.Info(ctx, "alert suppressed", "ruleID", rule.ID, "eventID", event.ID, "reason", string(why))
		return nil
	}

	payload := rep
	payload.Message = summary

	if err := b.SendAlert(ctx, rule.ConnectionID, payload); err != nil {
		// last_notified_at stays unset, so the next firing retries rather than
		// being silenced by the dedup window.
		return fmt.Errorf("sendalert: %w", err)
	}

	if err := b.storer.MarkNotified(ctx, event.ID, now); err != nil {
		return fmt.Errorf("marknotified: %w", err)
	}

	return nil
}

// matchPayloads counts the logs in a batch satisfying a condition and returns the
// most severe of them as the representative.
func matchPayloads(cond Condition, payloads []AlertPayload) (int, *AlertPayload) {
	var (
		matched int
		rep     *AlertPayload
	)

	for i := range payloads {
		p := &payloads[i]
		if !cond.MatchesLog(p.Level, p.Message, p.Source) {
			continue
		}
		matched++
		if rep == nil || severityRank(p.Level) > severityRank(rep.Level) {
			rep = p
		}
	}

	return matched, rep
}
