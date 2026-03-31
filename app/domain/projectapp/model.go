package projectapp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
)

// Project represents a project returned by the API.
type Project struct {
	ID          string `json:"id"`
	OrgID       string `json:"orgId"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app Project) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Projects is a slice of Project that implements web.Encoder.
type Projects []Project

// Encode implements the encoder interface.
func (app Projects) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppProject(bus projectbus.Project) Project {
	return Project{
		ID:          bus.ID.String(),
		OrgID:       bus.OrgID.String(),
		Name:        bus.Name,
		Color:       bus.Color,
		DateCreated: bus.DateCreated.Format(time.RFC3339),
		DateUpdated: bus.DateUpdated.Format(time.RFC3339),
	}
}

func toAppProjects(projects []projectbus.Project) Projects {
	app := make(Projects, len(projects))
	for i, p := range projects {
		app[i] = toAppProject(p)
	}
	return app
}

// =============================================================================

var colorRegEx = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// NewProject defines the data needed to create a new project.
type NewProject struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Decode implements the decoder interface.
func (app *NewProject) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusNewProject(app NewProject) (projectbus.NewProject, error) {
	var fieldErrors errs.FieldErrors

	if app.Name == "" {
		fieldErrors.Add("name", fmt.Errorf("name is required"))
	} else if len(app.Name) > 100 {
		fieldErrors.Add("name", fmt.Errorf("name must be 100 characters or fewer"))
	}

	if !colorRegEx.MatchString(app.Color) {
		fieldErrors.Add("color", fmt.Errorf("color must be a valid hex color (e.g. #f5a623)"))
	}

	if len(fieldErrors) > 0 {
		return projectbus.NewProject{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return projectbus.NewProject{
		Name:  app.Name,
		Color: app.Color,
	}, nil
}

// =============================================================================

// UpdateProject defines the data needed to update a project.
type UpdateProject struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// Decode implements the decoder interface.
func (app *UpdateProject) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusUpdateProject(app UpdateProject) (projectbus.UpdateProject, error) {
	var fieldErrors errs.FieldErrors

	if app.Name != nil {
		if *app.Name == "" {
			fieldErrors.Add("name", fmt.Errorf("name is required"))
		} else if len(*app.Name) > 100 {
			fieldErrors.Add("name", fmt.Errorf("name must be 100 characters or fewer"))
		}
	}

	if app.Color != nil {
		if !colorRegEx.MatchString(*app.Color) {
			fieldErrors.Add("color", fmt.Errorf("color must be a valid hex color (e.g. #f5a623)"))
		}
	}

	if len(fieldErrors) > 0 {
		return projectbus.UpdateProject{}, fmt.Errorf("validate: %w", fieldErrors.ToError())
	}

	return projectbus.UpdateProject{
		Name:  app.Name,
		Color: app.Color,
	}, nil
}
