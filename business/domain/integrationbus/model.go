// Package integrationbus provides business access to the integration domain.
package integrationbus

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ValidLevels is the set of accepted alert levels.
var ValidLevels = map[string]bool{
	"DEBUG": true,
	"INFO":  true,
	"WARN":  true,
	"ERROR": true,
}

// ProviderField defines one user-facing input on a provider form.
type ProviderField struct {
	Key         string `json:"k"`
	Label       string `json:"label"`
	Placeholder string `json:"ph,omitempty"`
}

// Provider is a definition of an integration type stored in integration_providers.
type Provider struct {
	ID          string
	Name        string
	Icon        string
	Type        string
	Description string
	Fields      []ProviderField
}

// Integration is a configured integration for a specific org.
type Integration struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProviderID  string
	Name        string
	Credentials map[string]string // decrypted; only in memory, never persisted in plain text
	Enabled     bool
	DateCreated time.Time
	DateUpdated time.Time
}

// NewIntegration contains the information needed to create a new integration.
type NewIntegration struct {
	OrgID       uuid.UUID
	ProviderID  string
	Name        string
	Credentials map[string]string
}

// UpdateIntegration contains optional updates for an existing integration.
type UpdateIntegration struct {
	Name        *string
	Credentials map[string]string
	Enabled     *bool
}

// AlertPayload is sent to a provider when an alert fires or during a connection test.
type AlertPayload struct {
	ProjectName string
	Level       string
	Message     string
	Source      string
	LogID       string
	Timestamp   time.Time
}

// Caller is implemented by each integration provider to deliver an alert.
type Caller interface {
	Send(ctx context.Context, creds map[string]string, payload AlertPayload) error
}

// =============================================================================
// Alert Rules

// AlertRule is a configured alert rule for an org.
type AlertRule struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	ConnectionID uuid.UUID
	ProjectID    *uuid.UUID
	Name         string
	Level        string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewAlertRule contains the information needed to create a new alert rule.
type NewAlertRule struct {
	OrgID        uuid.UUID
	ConnectionID uuid.UUID
	ProjectID    *uuid.UUID
	Name         string
	Level        string
	IsActive     bool
}

// UpdateAlertRule contains the optional fields that can be updated on a rule.
type UpdateAlertRule struct {
	Name         *string
	Level        *string
	ConnectionID *uuid.UUID
	ProjectID    **uuid.UUID // outer pointer = field present; inner pointer = nullable value
	IsActive     *bool
}
