package integrationapp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// =============================================================================
// Provider (catalog)

// AppProviderField is the API representation of a provider form field.
type AppProviderField struct {
	Key         string `json:"k"`
	Label       string `json:"label"`
	Placeholder string `json:"ph,omitempty"`
}

// AppProvider is the API representation of an integration provider definition.
type AppProvider struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Icon        string             `json:"icon"`
	Type        string             `json:"type"`
	Description string             `json:"desc"`
	Fields      []AppProviderField `json:"fields"`
}

// providersResponse wraps the provider list in the shape the frontend expects.
type providersResponse struct {
	Providers []AppProvider `json:"providers"`
}

// Encode implements web.Encoder.
func (p providersResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(p)
	return data, "application/json", err
}

func toAppProvider(bus integrationbus.Provider) AppProvider {
	fields := make([]AppProviderField, len(bus.Fields))
	for i, f := range bus.Fields {
		fields[i] = AppProviderField{
			Key:         f.Key,
			Label:       f.Label,
			Placeholder: f.Placeholder,
		}
	}
	return AppProvider{
		ID:          bus.ID,
		Name:        bus.Name,
		Icon:        bus.Icon,
		Type:        bus.Type,
		Description: bus.Description,
		Fields:      fields,
	}
}

func toAppProviders(providers []integrationbus.Provider) providersResponse {
	list := make([]AppProvider, len(providers))
	for i, p := range providers {
		list[i] = toAppProvider(p)
	}
	return providersResponse{Providers: list}
}

// =============================================================================
// Integration (per-org configured)

// AppIntegration is the API representation of a configured connection.
// Credentials are never included in responses.
type AppIntegration struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
	IsActive  bool   `json:"isActive"`
	CreatedAt string `json:"createdAt"`
}

// connectionsResponse wraps the integration list in the shape the frontend expects.
type connectionsResponse struct {
	Connections []AppIntegration `json:"connections"`
}

// Encode implements web.Encoder.
func (c connectionsResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(c)
	return data, "application/json", err
}

// Encode implements web.Encoder.
func (a AppIntegration) Encode() ([]byte, string, error) {
	data, err := json.Marshal(a)
	return data, "application/json", err
}

func toAppIntegration(bus integrationbus.Integration) AppIntegration {
	return AppIntegration{
		ID:        bus.ID.String(),
		Provider:  bus.ProviderID,
		Name:      bus.Name,
		ProjectID: bus.ProjectID.String(),
		IsActive:  bus.Enabled,
		CreatedAt: bus.DateCreated.UTC().Format(time.RFC3339),
	}
}

func toAppIntegrations(list []integrationbus.Integration) connectionsResponse {
	connections := make([]AppIntegration, len(list))
	for i, v := range list {
		connections[i] = toAppIntegration(v)
	}
	return connectionsResponse{Connections: connections}
}

// =============================================================================
// Request types — integrations

// NewIntegrationRequest is the POST body for creating an integration.
type NewIntegrationRequest struct {
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Credentials map[string]string `json:"credentials"`
}

// Decode implements web.Decoder.
func (r *NewIntegrationRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}

func toBusNewIntegration(app NewIntegrationRequest) (integrationbus.NewIntegration, error) {
	var fieldErrors errs.FieldErrors

	if app.Provider == "" {
		fieldErrors.Add("provider", fmt.Errorf("provider is required"))
	}
	if app.Name == "" {
		fieldErrors.Add("name", fmt.Errorf("name is required"))
	}
	if len(app.Credentials) == 0 {
		fieldErrors.Add("credentials", fmt.Errorf("credentials are required"))
	}

	if len(fieldErrors) > 0 {
		return integrationbus.NewIntegration{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return integrationbus.NewIntegration{
		ProviderID:  app.Provider,
		Name:        app.Name,
		Credentials: app.Credentials,
	}, nil
}

// UpdateIntegrationRequest is the PUT body for updating an integration.
type UpdateIntegrationRequest struct {
	Name        *string           `json:"name"`
	Credentials map[string]string `json:"credentials"`
	Enabled     *bool             `json:"enabled"`
}

// Decode implements web.Decoder.
func (r *UpdateIntegrationRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}

func toBusUpdateIntegration(app UpdateIntegrationRequest) integrationbus.UpdateIntegration {
	return integrationbus.UpdateIntegration{
		Name:        app.Name,
		Credentials: app.Credentials,
		Enabled:     app.Enabled,
	}
}

// disconnectedResponse is returned when an integration is soft-deleted.
type disconnectedResponse struct {
	Disconnected bool `json:"disconnected"`
}

// Encode implements web.Encoder.
func (d disconnectedResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(d)
	return data, "application/json", err
}

// testResponse is returned when a connection test succeeds.
type testResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// Encode implements web.Encoder.
func (t testResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(t)
	return data, "application/json", err
}

// =============================================================================
// Alert Rule app models

// AppAlertRule is the API representation of an alert rule. Rules are team-
// visible; userId/owner* are the creator, for display only.
type AppAlertRule struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Level        string  `json:"level"`
	ConnectionID string  `json:"connectionId"`
	ProjectID    string  `json:"projectId"`
	IsActive     bool    `json:"isActive"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt,omitempty"`
	UserID       *string `json:"userId"`
	OwnerName    string  `json:"ownerName,omitempty"`
	OwnerEmail   string  `json:"ownerEmail,omitempty"`

	// Condition is what actually decides whether the rule fires; Level is kept
	// for display and for rules created before conditions existed.
	Condition          json.RawMessage `json:"condition"`
	DedupWindowSeconds int             `json:"dedupWindowSeconds"`
	SnoozeUntil        *string         `json:"snoozeUntil"`
}

// ruleResponse wraps a single rule as { "rule": {...} }.
type ruleResponse struct {
	Rule AppAlertRule `json:"rule"`
}

// Encode implements web.Encoder.
func (r ruleResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// toggleRuleResponse is returned from the toggle endpoint.
type toggleRuleResponse struct {
	ID       string `json:"id"`
	IsActive bool   `json:"isActive"`
}

// Encode implements web.Encoder.
func (t toggleRuleResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(t)
	return data, "application/json", err
}

// deleteRuleResponse is returned when a rule is deleted.
type deleteRuleResponse struct {
	Deleted bool `json:"deleted"`
}

// Encode implements web.Encoder.
func (d deleteRuleResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(d)
	return data, "application/json", err
}

func toAppAlertRule(bus integrationbus.AlertRule) AppAlertRule {
	r := AppAlertRule{
		ID:           bus.ID.String(),
		Name:         bus.Name,
		Level:        bus.Level,
		ConnectionID: bus.ConnectionID.String(),
		ProjectID:    bus.ProjectID.String(),
		IsActive:     bus.IsActive,
		CreatedAt:    bus.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    bus.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if bus.UserID != nil {
		s := bus.UserID.String()
		r.UserID = &s
	}

	r.DedupWindowSeconds = int(bus.DedupWindow().Seconds())

	if encoded, err := integrationbus.MarshalCondition(bus.Condition); err == nil {
		r.Condition = encoded
	}

	if bus.SnoozeUntil != nil {
		v := bus.SnoozeUntil.UTC().Format(time.RFC3339)
		r.SnoozeUntil = &v
	}

	return r
}

// =============================================================================
// Request types — alert rules

// NewRuleRequest is the POST body for creating an alert rule. The rule's
// project comes from the route and its owner from the caller — neither is
// accepted from the client.
type NewRuleRequest struct {
	Name         string `json:"name"`
	Level        string `json:"level"`
	ConnectionID string `json:"connectionId"`
	IsActive     bool   `json:"isActive"`

	// Condition may be omitted, in which case the rule fires at or above Level —
	// the behaviour every rule had before conditions existed.
	Condition          json.RawMessage `json:"condition"`
	DedupWindowSeconds int             `json:"dedupWindowSeconds"`
}

// Decode implements web.Decoder.
func (r *NewRuleRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}

func toBusNewRule(orgID, projectID, userID uuid.UUID, req NewRuleRequest) (integrationbus.NewAlertRule, error) {
	var fieldErrors errs.FieldErrors

	if req.Name == "" {
		fieldErrors.Add("name", fmt.Errorf("name is required"))
	}
	if req.Level == "" {
		fieldErrors.Add("level", fmt.Errorf("level is required"))
	}

	// An omitted condition means "fire at or above level", which is what every
	// rule did before conditions existed.
	cond := integrationbus.LevelCondition(req.Level)
	if len(req.Condition) > 0 {
		parsed, err := integrationbus.ParseCondition(req.Condition)
		if err != nil {
			fieldErrors.Add("condition", err)
		} else {
			cond = parsed
		}
	}
	if req.ConnectionID == "" {
		fieldErrors.Add("connectionId", fmt.Errorf("connectionId is required"))
	}

	if len(fieldErrors) > 0 {
		return integrationbus.NewAlertRule{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	connID, err := uuid.Parse(req.ConnectionID)
	if err != nil {
		return integrationbus.NewAlertRule{}, fmt.Errorf("validate: connectionId: %w", errs.NewFieldErrors("connectionId", err))
	}

	return integrationbus.NewAlertRule{
		OrgID:              orgID,
		ProjectID:          projectID,
		ConnectionID:       connID,
		UserID:             userID,
		Condition:          cond,
		DedupWindowSeconds: req.DedupWindowSeconds,
		Name:               req.Name,
		Level:              req.Level,
		IsActive:           req.IsActive,
	}, nil
}

// UpdateRuleRequest is the PUT body for updating a rule. project and owner are
// immutable and cannot be changed here.
type UpdateRuleRequest struct {
	Name         *string `json:"name"`
	Level        *string `json:"level"`
	ConnectionID *string `json:"connectionId"`
	IsActive     *bool   `json:"isActive"`

	Condition          json.RawMessage `json:"condition"`
	DedupWindowSeconds *int            `json:"dedupWindowSeconds"`

	// SnoozeUntil silences the rule until an instant; null clears the snooze.
	SnoozeUntil json.RawMessage `json:"snoozeUntil"`
}

// Decode implements web.Decoder.
func (r *UpdateRuleRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}

func toBusUpdateRule(req UpdateRuleRequest) (integrationbus.UpdateAlertRule, error) {
	ur := integrationbus.UpdateAlertRule{
		Name:               req.Name,
		Level:              req.Level,
		IsActive:           req.IsActive,
		DedupWindowSeconds: req.DedupWindowSeconds,
	}

	if len(req.Condition) > 0 {
		cond, err := integrationbus.ParseCondition(req.Condition)
		if err != nil {
			return integrationbus.UpdateAlertRule{}, errs.NewFieldErrors("condition", err)
		}
		ur.Condition = &cond
	}

	if len(req.SnoozeUntil) > 0 {
		if string(req.SnoozeUntil) == "null" {
			var cleared *time.Time
			ur.SnoozeUntil = &cleared
		} else {
			var raw string
			if err := json.Unmarshal(req.SnoozeUntil, &raw); err != nil {
				return integrationbus.UpdateAlertRule{}, errs.NewFieldErrors("snoozeUntil", fmt.Errorf("must be an RFC3339 string or null"))
			}
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return integrationbus.UpdateAlertRule{}, errs.NewFieldErrors("snoozeUntil", fmt.Errorf("must be an RFC3339 string or null"))
			}
			at := &t
			ur.SnoozeUntil = &at
		}
	}

	if req.ConnectionID != nil {
		id, err := uuid.Parse(*req.ConnectionID)
		if err != nil {
			return integrationbus.UpdateAlertRule{}, fmt.Errorf("validate: connectionId: %w", errs.NewFieldErrors("connectionId", err))
		}
		ur.ConnectionID = &id
	}

	return ur, nil
}

// ToggleRuleRequest is the PATCH body for toggling a rule.
type ToggleRuleRequest struct {
	IsActive bool `json:"isActive"`
}

// Decode implements web.Decoder.
func (r *ToggleRuleRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}
