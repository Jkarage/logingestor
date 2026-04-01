package orgapp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
)

// Org represents an organization returned by the API.
type Org struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Enabled     bool   `json:"enabled"`
	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app Org) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppOrg(bus orgbus.Org) Org {
	return Org{
		ID:          bus.ID.String(),
		Name:        bus.Name.String(),
		Slug:        bus.Slug,
		Enabled:     bus.Enabled,
		DateCreated: bus.DateCreated.Format(time.RFC3339),
		DateUpdated: bus.DateUpdated.Format(time.RFC3339),
	}
}

func toAppOrgs(orgs []orgbus.Org) []Org {
	app := make([]Org, len(orgs))
	for i, o := range orgs {
		app[i] = toAppOrg(o)
	}
	return app
}

// =============================================================================

// OrgMember is the API representation of a member with their user profile.
type OrgMember struct {
	MemberID     string `json:"memberID"`
	UserID       string `json:"userID"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	Enabled      bool   `json:"enabled"`
	JoinedAt     string `json:"joinedAt"`
	ProjectCount int    `json:"projectCount"`
}

// Encode implements the encoder interface.
func (app OrgMember) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// OrgMembers is a slice of OrgMember that implements web.Encoder.
type OrgMembers []OrgMember

// Encode implements the encoder interface.
func (app OrgMembers) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppOrgMembers(bus []orgbus.OrgMemberUser) OrgMembers {
	members := make(OrgMembers, len(bus))
	for i, m := range bus {
		members[i] = OrgMember{
			MemberID:     m.MemberID.String(),
			UserID:       m.UserID.String(),
			Name:         m.Name.String(),
			Email:        m.Email,
			Role:         m.Role.String(),
			Enabled:      m.Enabled,
			JoinedAt:     m.JoinedAt.Format(time.RFC3339),
			ProjectCount: m.ProjectCount,
		}
	}
	return members
}

// UserOrg is the org response enriched with the caller's membership role.
// The frontend uses this to know which workspaces the user can switch into
// and what actions they are allowed to take in each one.
type UserOrg struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Enabled     bool   `json:"enabled"`
	Role        string `json:"role"`
	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app UserOrg) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppUserOrg(bus orgbus.UserOrg) UserOrg {
	return UserOrg{
		ID:          bus.ID.String(),
		Name:        bus.Name.String(),
		Slug:        bus.Slug,
		Enabled:     bus.Enabled,
		Role:        bus.Role.String(),
		DateCreated: bus.DateCreated.Format(time.RFC3339),
		DateUpdated: bus.DateUpdated.Format(time.RFC3339),
	}
}

// UserOrgs is a slice of UserOrg that implements web.Encoder.
type UserOrgs []UserOrg

// Encode implements the encoder interface.
func (app UserOrgs) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppUserOrgs(orgs []orgbus.UserOrg) UserOrgs {
	app := make(UserOrgs, len(orgs))
	for i, o := range orgs {
		app[i] = toAppUserOrg(o)
	}
	return app
}

// =============================================================================

// NewOrg defines the data needed to create a new organization.
type NewOrg struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Decode implements the decoder interface.
func (app *NewOrg) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusNewOrg(app NewOrg) (orgbus.NewOrg, error) {
	var fieldErrors errs.FieldErrors

	nme, err := name.Parse(app.Name)
	if err != nil {
		fieldErrors.Add("name", err)
	}

	if app.Slug == "" {
		fieldErrors.Add("slug", fmt.Errorf("slug is required"))
	}

	if len(fieldErrors) > 0 {
		return orgbus.NewOrg{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return orgbus.NewOrg{
		Name: nme,
		Slug: app.Slug,
	}, nil
}

// =============================================================================

// UpdateOrg defines the data needed to update an organization.
type UpdateOrg struct {
	Name    *string `json:"name"`
	Slug    *string `json:"slug"`
	Enabled *bool   `json:"enabled"`
}

// Decode implements the decoder interface.
func (app *UpdateOrg) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusUpdateOrg(app UpdateOrg) (orgbus.UpdateOrg, error) {
	var fieldErrors errs.FieldErrors

	var nme *name.Name
	if app.Name != nil {
		nm, err := name.Parse(*app.Name)
		if err != nil {
			fieldErrors.Add("name", err)
		}
		nme = &nm
	}

	if len(fieldErrors) > 0 {
		return orgbus.UpdateOrg{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return orgbus.UpdateOrg{
		Name:    nme,
		Slug:    app.Slug,
		Enabled: app.Enabled,
	}, nil
}

// =============================================================================

// UpdateOrgRole defines the data needed to update a member's role within an org.
type UpdateOrgRole struct {
	MemberID string `json:"memberID"`
	Role     string `json:"role"`
}

// Decode implements the decoder interface.
func (app *UpdateOrgRole) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusUpdateOrgRole(app UpdateOrgRole) (orgbus.UpdateOrgMember, error) {
	var fieldErrors errs.FieldErrors

	r, err := role.Parse(app.Role)
	if err != nil {
		fieldErrors.Add("role", err)
	}

	if len(fieldErrors) > 0 {
		return orgbus.UpdateOrgMember{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return orgbus.UpdateOrgMember{Role: r}, nil
}
