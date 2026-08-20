// Package viewapp maintains the app layer api for saved views and dashboards.
package viewapp

import (
	"encoding/json"
	"time"

	"github.com/jkarage/logingestor/business/domain/viewbus"
)

// SavedView is a saved log query as returned by the API.
type SavedView struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"orgId"`
	ProjectID   *string         `json:"projectId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Query       json.RawMessage `json:"query"`
	Visibility  string          `json:"visibility"`
	CreatedBy   string          `json:"createdBy"`

	// CanEdit tells the client whether to offer edit and delete, so it does not
	// have to reimplement the ownership rule.
	CanEdit bool `json:"canEdit"`

	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app SavedView) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// SavedViews is the list response shape.
type SavedViews struct {
	SavedViews []SavedView `json:"savedViews"`
}

// Encode implements the encoder interface.
func (app SavedViews) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppSavedView(v viewbus.SavedView, canEdit bool) SavedView {
	var projectID *string
	if v.ProjectID != nil {
		s := v.ProjectID.String()
		projectID = &s
	}

	query := v.Query
	if len(query) == 0 {
		query = json.RawMessage("{}")
	}

	return SavedView{
		ID:          v.ID.String(),
		OrgID:       v.OrgID.String(),
		ProjectID:   projectID,
		Name:        v.Name,
		Description: v.Description,
		Query:       query,
		Visibility:  v.Visibility,
		CreatedBy:   v.CreatedBy.String(),
		CanEdit:     canEdit,
		DateCreated: v.DateCreated.Format(time.RFC3339),
		DateUpdated: v.DateUpdated.Format(time.RFC3339),
	}
}

// NewSavedView is the create request body.
type NewSavedView struct {
	ProjectID   string          `json:"projectId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Query       json.RawMessage `json:"query"`
	Visibility  string          `json:"visibility"`
}

// Decode implements the decoder interface.
func (app *NewSavedView) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// UpdateSavedView is the update request body. Omitted fields are unchanged; a
// null projectId clears the project pin.
type UpdateSavedView struct {
	ProjectID   json.RawMessage `json:"projectId"`
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Query       json.RawMessage `json:"query"`
	Visibility  *string         `json:"visibility"`
}

// Decode implements the decoder interface.
func (app *UpdateSavedView) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// =============================================================================

// Dashboard is a dashboard as returned by the API.
type Dashboard struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"orgId"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Panels      json.RawMessage `json:"panels"`
	Visibility  string          `json:"visibility"`
	CreatedBy   string          `json:"createdBy"`
	CanEdit     bool            `json:"canEdit"`
	DateCreated string          `json:"dateCreated"`
	DateUpdated string          `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app Dashboard) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Dashboards is the list response shape.
type Dashboards struct {
	Dashboards []Dashboard `json:"dashboards"`
}

// Encode implements the encoder interface.
func (app Dashboards) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppDashboard(d viewbus.Dashboard, canEdit bool) Dashboard {
	panels := d.Panels
	if len(panels) == 0 {
		panels = json.RawMessage("[]")
	}

	return Dashboard{
		ID:          d.ID.String(),
		OrgID:       d.OrgID.String(),
		Name:        d.Name,
		Description: d.Description,
		Panels:      panels,
		Visibility:  d.Visibility,
		CreatedBy:   d.CreatedBy.String(),
		CanEdit:     canEdit,
		DateCreated: d.DateCreated.Format(time.RFC3339),
		DateUpdated: d.DateUpdated.Format(time.RFC3339),
	}
}

// NewDashboard is the create request body.
type NewDashboard struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Panels      json.RawMessage `json:"panels"`
	Visibility  string          `json:"visibility"`
}

// Decode implements the decoder interface.
func (app *NewDashboard) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// UpdateDashboard is the update request body.
type UpdateDashboard struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Panels      json.RawMessage `json:"panels"`
	Visibility  *string         `json:"visibility"`
}

// Decode implements the decoder interface.
func (app *UpdateDashboard) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}
