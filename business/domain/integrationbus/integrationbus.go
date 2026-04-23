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
	ErrNotFound          = errors.New("integration not found")
	ErrDuplicateName     = errors.New("integration name already exists for this provider in org")
	ErrUnknownProvider   = errors.New("unknown integration provider")
	ErrProviderRejected  = errors.New("provider rejected the request")
	ErrRuleNotFound      = errors.New("alert rule not found")
	ErrInvalidLevel      = errors.New("level must be one of DEBUG, INFO, WARN, ERROR")
	ErrConnectionBadOrg  = errors.New("connection does not belong to this org")
)

// Storer declares the persistence behaviour this package needs.
type Storer interface {
	Create(ctx context.Context, i Integration) error
	Update(ctx context.Context, i Integration) error
	Delete(ctx context.Context, i Integration) error
	QueryByID(ctx context.Context, id uuid.UUID) (Integration, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Integration, error)
	QueryProviders(ctx context.Context) ([]Provider, error)

	CreateRule(ctx context.Context, r AlertRule) error
	UpdateRule(ctx context.Context, r AlertRule) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	QueryRuleByID(ctx context.Context, id uuid.UUID) (AlertRule, error)
	QueryRulesByOrg(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error)
	DisableRulesByConnection(ctx context.Context, connectionID uuid.UUID) error
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

// QueryByOrg returns all integrations configured for an org.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Integration, error) {
	integrations, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querybyorg: %w", err)
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
		Message:     "This is a test alert from LoginGestor. Your integration is working correctly.",
		Source:      "logingestor/test",
		LogID:       "00000000-0000-0000-0000-000000000000",
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

	if conn.OrgID != nr.OrgID {
		return AlertRule{}, fmt.Errorf("createrule: %w", ErrConnectionBadOrg)
	}

	now := time.Now()
	r := AlertRule{
		ID:           uuid.New(),
		OrgID:        nr.OrgID,
		ConnectionID: nr.ConnectionID,
		ProjectID:    nr.ProjectID,
		Name:         nr.Name,
		Level:        nr.Level,
		IsActive:     nr.IsActive,
		CreatedAt:    now,
		UpdatedAt:    now,
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
		r.ConnectionID = *ur.ConnectionID
	}
	if ur.ProjectID != nil {
		r.ProjectID = *ur.ProjectID
	}
	if ur.IsActive != nil {
		r.IsActive = *ur.IsActive
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

// QueryRulesByOrg returns all alert rules for an org.
func (b *Business) QueryRulesByOrg(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rules, err := b.storer.QueryRulesByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("queryrulesbyorg: %w", err)
	}
	return rules, nil
}
