package integrationapp

import (
	"encoding/json"
	"fmt"
	"time"

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
	Description string             `json:"description"`
	Fields      []AppProviderField `json:"fields"`
}

// Encode implements web.Encoder.
func (a AppProvider) Encode() ([]byte, string, error) {
	data, err := json.Marshal(a)
	return data, "application/json", err
}

// AppProviders is a slice of AppProvider that implements web.Encoder.
type AppProviders []AppProvider

// Encode implements web.Encoder.
func (a AppProviders) Encode() ([]byte, string, error) {
	data, err := json.Marshal(a)
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

func toAppProviders(providers []integrationbus.Provider) AppProviders {
	app := make(AppProviders, len(providers))
	for i, p := range providers {
		app[i] = toAppProvider(p)
	}
	return app
}

// =============================================================================
// Integration (per-org configured)

// AppIntegration is the API representation of a configured integration.
// Credentials are never included in responses.
type AppIntegration struct {
	ID          string `json:"id"`
	OrgID       string `json:"orgId"`
	Provider    string `json:"provider"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements web.Encoder.
func (a AppIntegration) Encode() ([]byte, string, error) {
	data, err := json.Marshal(a)
	return data, "application/json", err
}

// AppIntegrations is a slice of AppIntegration that implements web.Encoder.
type AppIntegrations []AppIntegration

// Encode implements web.Encoder.
func (a AppIntegrations) Encode() ([]byte, string, error) {
	data, err := json.Marshal(a)
	return data, "application/json", err
}

func toAppIntegration(bus integrationbus.Integration) AppIntegration {
	return AppIntegration{
		ID:          bus.ID.String(),
		OrgID:       bus.OrgID.String(),
		Provider:    bus.ProviderID,
		Name:        bus.Name,
		Enabled:     bus.Enabled,
		DateCreated: bus.DateCreated.Format(time.RFC3339),
		DateUpdated: bus.DateUpdated.Format(time.RFC3339),
	}
}

func toAppIntegrations(list []integrationbus.Integration) AppIntegrations {
	app := make(AppIntegrations, len(list))
	for i, v := range list {
		app[i] = toAppIntegration(v)
	}
	return app
}

// =============================================================================
// Request types

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
