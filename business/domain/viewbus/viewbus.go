// Package viewbus manages saved log views and dashboards.
//
// Both are a named, owned, org-scoped JSON document and share one visibility
// rule, so the access decisions live here once rather than in two places that
// could drift apart.
package viewbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("not found")
	ErrNameRequired  = errors.New("name is required")
	ErrNameTooLong   = fmt.Errorf("name must be at most %d characters", MaxNameLen)
	ErrDefTooLarge   = fmt.Errorf("definition must be at most %d bytes", MaxDefinitionBytes)
	ErrBadVisibility = fmt.Errorf("visibility must be %q or %q", VisibilityPrivate, VisibilityOrg)
	ErrPanelsNotList = errors.New("panels must be a JSON array")
)

// Limits on what callers may store. The definition is opaque, so size is the
// only thing worth policing.
const (
	MaxNameLen         = 120
	MaxDescriptionLen  = 2000
	MaxDefinitionBytes = 64 * 1024
)

// Visibility values.
const (
	VisibilityPrivate = "private"
	VisibilityOrg     = "org"
)

// ParseVisibility validates a submitted visibility, defaulting to org so a
// saved view is shareable unless the caller opts out.
func ParseVisibility(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case "":
		return VisibilityOrg, nil
	case VisibilityPrivate:
		return VisibilityPrivate, nil
	case VisibilityOrg:
		return VisibilityOrg, nil
	}
	return "", ErrBadVisibility
}

// SavedView is a named log query.
type SavedView struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProjectID   *uuid.UUID
	Name        string
	Description string
	Query       json.RawMessage
	Visibility  string
	CreatedBy   uuid.UUID
	DateCreated time.Time
	DateUpdated time.Time
}

// Dashboard is a named collection of panels.
type Dashboard struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	Panels      json.RawMessage
	Visibility  string
	CreatedBy   uuid.UUID
	DateCreated time.Time
	DateUpdated time.Time
}

// Viewer describes the caller for an access decision.
type Viewer struct {
	UserID uuid.UUID

	// OrgAdmin is true when the caller administers the org holding the record.
	OrgAdmin bool

	// VisibleProjects is the set of projects the caller may read. It gates
	// project-pinned saved views; dashboards are not project-scoped.
	VisibleProjects map[uuid.UUID]struct{}
}

// CanSeeView reports whether the viewer may read a saved view.
//
// A private view is the creator's alone — an org admin does not get to read it,
// because "private" would mean nothing if it did. A project-pinned view is also
// hidden from anyone who cannot read that project, otherwise the name and query
// would leak which projects exist and what is in them.
func CanSeeView(v SavedView, who Viewer) bool {
	if v.Visibility == VisibilityPrivate && v.CreatedBy != who.UserID {
		return false
	}

	if v.ProjectID != nil {
		if _, ok := who.VisibleProjects[*v.ProjectID]; !ok {
			return false
		}
	}

	return true
}

// CanSeeDashboard reports whether the viewer may read a dashboard.
func CanSeeDashboard(d Dashboard, who Viewer) bool {
	return d.Visibility != VisibilityPrivate || d.CreatedBy == who.UserID
}

// CanModify reports whether the viewer may change or delete a record.
//
// Org admins may modify anything shared with the org so a team is not stuck with
// a colleague's stale dashboard, but not somebody's private one — being an admin
// is not a reason to reach into private work.
func CanModify(createdBy uuid.UUID, visibility string, who Viewer) bool {
	if createdBy == who.UserID {
		return true
	}
	return who.OrgAdmin && visibility != VisibilityPrivate
}

// NewSavedView is the data needed to create a saved view.
type NewSavedView struct {
	ProjectID   *uuid.UUID
	Name        string
	Description string
	Query       json.RawMessage
	Visibility  string
}

// UpdateSavedView carries the fields that may change. A nil field is untouched.
type UpdateSavedView struct {
	Name        *string
	Description *string
	Query       json.RawMessage
	Visibility  *string

	// ProjectID is a double pointer so clearing the pin is distinguishable from
	// leaving it alone.
	ProjectID **uuid.UUID
}

// NewDashboard is the data needed to create a dashboard.
type NewDashboard struct {
	Name        string
	Description string
	Panels      json.RawMessage
	Visibility  string
}

// UpdateDashboard carries the fields that may change.
type UpdateDashboard struct {
	Name        *string
	Description *string
	Panels      json.RawMessage
	Visibility  *string
}

// validateName checks a submitted name.
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", ErrNameRequired
	case len(name) > MaxNameLen:
		return "", ErrNameTooLong
	}
	return name, nil
}

// validateDefinition checks an opaque JSON definition, defaulting to fallback
// when absent so a record always holds valid JSON.
func validateDefinition(def json.RawMessage, fallback string, mustBeArray bool) (json.RawMessage, error) {
	if len(def) == 0 {
		return json.RawMessage(fallback), nil
	}

	if len(def) > MaxDefinitionBytes {
		return nil, ErrDefTooLarge
	}

	var probe any
	if err := json.Unmarshal(def, &probe); err != nil {
		return nil, fmt.Errorf("definition is not valid JSON: %w", err)
	}

	if mustBeArray {
		if _, ok := probe.([]any); !ok {
			return nil, ErrPanelsNotList
		}
	}

	return def, nil
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	CreateView(ctx context.Context, v SavedView) error
	UpdateView(ctx context.Context, v SavedView) error
	DeleteView(ctx context.Context, id uuid.UUID) error
	QueryViewByID(ctx context.Context, id uuid.UUID) (SavedView, error)
	QueryViewsByOrg(ctx context.Context, orgID uuid.UUID) ([]SavedView, error)

	CreateDashboard(ctx context.Context, d Dashboard) error
	UpdateDashboard(ctx context.Context, d Dashboard) error
	DeleteDashboard(ctx context.Context, id uuid.UUID) error
	QueryDashboardByID(ctx context.Context, id uuid.UUID) (Dashboard, error)
	QueryDashboardsByOrg(ctx context.Context, orgID uuid.UUID) ([]Dashboard, error)
}

// Business manages the set of APIs for saved views and dashboards.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs a view business API.
func NewBusiness(log *logger.Logger, storer Storer) *Business {
	return &Business{log: log, storer: storer}
}

// CreateView stores a new saved view owned by actorID.
func (b *Business) CreateView(ctx context.Context, orgID, actorID uuid.UUID, nv NewSavedView) (SavedView, error) {
	name, err := validateName(nv.Name)
	if err != nil {
		return SavedView{}, err
	}

	query, err := validateDefinition(nv.Query, "{}", false)
	if err != nil {
		return SavedView{}, err
	}

	vis, err := ParseVisibility(nv.Visibility)
	if err != nil {
		return SavedView{}, err
	}

	now := time.Now()

	v := SavedView{
		ID:          uuid.New(),
		OrgID:       orgID,
		ProjectID:   nv.ProjectID,
		Name:        name,
		Description: truncate(nv.Description, MaxDescriptionLen),
		Query:       query,
		Visibility:  vis,
		CreatedBy:   actorID,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.CreateView(ctx, v); err != nil {
		return SavedView{}, fmt.Errorf("createview: %w", err)
	}

	return v, nil
}

// UpdateView applies changes to an existing saved view.
func (b *Business) UpdateView(ctx context.Context, v SavedView, uv UpdateSavedView) (SavedView, error) {
	if uv.Name != nil {
		name, err := validateName(*uv.Name)
		if err != nil {
			return SavedView{}, err
		}
		v.Name = name
	}

	if uv.Description != nil {
		v.Description = truncate(*uv.Description, MaxDescriptionLen)
	}

	if len(uv.Query) > 0 {
		query, err := validateDefinition(uv.Query, "{}", false)
		if err != nil {
			return SavedView{}, err
		}
		v.Query = query
	}

	if uv.Visibility != nil {
		vis, err := ParseVisibility(*uv.Visibility)
		if err != nil {
			return SavedView{}, err
		}
		v.Visibility = vis
	}

	if uv.ProjectID != nil {
		v.ProjectID = *uv.ProjectID
	}

	v.DateUpdated = time.Now()

	if err := b.storer.UpdateView(ctx, v); err != nil {
		return SavedView{}, fmt.Errorf("updateview: %w", err)
	}

	return v, nil
}

// DeleteView removes a saved view.
func (b *Business) DeleteView(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.DeleteView(ctx, id); err != nil {
		return fmt.Errorf("deleteview: %w", err)
	}
	return nil
}

// QueryViewByID returns one saved view.
func (b *Business) QueryViewByID(ctx context.Context, id uuid.UUID) (SavedView, error) {
	v, err := b.storer.QueryViewByID(ctx, id)
	if err != nil {
		return SavedView{}, fmt.Errorf("queryviewbyid: %w", err)
	}
	return v, nil
}

// QueryViewsVisible returns the org's saved views the viewer may read.
func (b *Business) QueryViewsVisible(ctx context.Context, orgID uuid.UUID, who Viewer) ([]SavedView, error) {
	all, err := b.storer.QueryViewsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("queryviewsbyorg: %w", err)
	}

	out := make([]SavedView, 0, len(all))
	for _, v := range all {
		if CanSeeView(v, who) {
			out = append(out, v)
		}
	}

	return out, nil
}

// CreateDashboard stores a new dashboard owned by actorID.
func (b *Business) CreateDashboard(ctx context.Context, orgID, actorID uuid.UUID, nd NewDashboard) (Dashboard, error) {
	name, err := validateName(nd.Name)
	if err != nil {
		return Dashboard{}, err
	}

	panels, err := validateDefinition(nd.Panels, "[]", true)
	if err != nil {
		return Dashboard{}, err
	}

	vis, err := ParseVisibility(nd.Visibility)
	if err != nil {
		return Dashboard{}, err
	}

	now := time.Now()

	d := Dashboard{
		ID:          uuid.New(),
		OrgID:       orgID,
		Name:        name,
		Description: truncate(nd.Description, MaxDescriptionLen),
		Panels:      panels,
		Visibility:  vis,
		CreatedBy:   actorID,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.CreateDashboard(ctx, d); err != nil {
		return Dashboard{}, fmt.Errorf("createdashboard: %w", err)
	}

	return d, nil
}

// UpdateDashboard applies changes to an existing dashboard.
func (b *Business) UpdateDashboard(ctx context.Context, d Dashboard, ud UpdateDashboard) (Dashboard, error) {
	if ud.Name != nil {
		name, err := validateName(*ud.Name)
		if err != nil {
			return Dashboard{}, err
		}
		d.Name = name
	}

	if ud.Description != nil {
		d.Description = truncate(*ud.Description, MaxDescriptionLen)
	}

	if len(ud.Panels) > 0 {
		panels, err := validateDefinition(ud.Panels, "[]", true)
		if err != nil {
			return Dashboard{}, err
		}
		d.Panels = panels
	}

	if ud.Visibility != nil {
		vis, err := ParseVisibility(*ud.Visibility)
		if err != nil {
			return Dashboard{}, err
		}
		d.Visibility = vis
	}

	d.DateUpdated = time.Now()

	if err := b.storer.UpdateDashboard(ctx, d); err != nil {
		return Dashboard{}, fmt.Errorf("updatedashboard: %w", err)
	}

	return d, nil
}

// DeleteDashboard removes a dashboard.
func (b *Business) DeleteDashboard(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.DeleteDashboard(ctx, id); err != nil {
		return fmt.Errorf("deletedashboard: %w", err)
	}
	return nil
}

// QueryDashboardByID returns one dashboard.
func (b *Business) QueryDashboardByID(ctx context.Context, id uuid.UUID) (Dashboard, error) {
	d, err := b.storer.QueryDashboardByID(ctx, id)
	if err != nil {
		return Dashboard{}, fmt.Errorf("querydashboardbyid: %w", err)
	}
	return d, nil
}

// QueryDashboardsVisible returns the org's dashboards the viewer may read.
func (b *Business) QueryDashboardsVisible(ctx context.Context, orgID uuid.UUID, who Viewer) ([]Dashboard, error) {
	all, err := b.storer.QueryDashboardsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querydashboardsbyorg: %w", err)
	}

	out := make([]Dashboard, 0, len(all))
	for _, d := range all {
		if CanSeeDashboard(d, who) {
			out = append(out, d)
		}
	}

	return out, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max]
	}
	return s
}
