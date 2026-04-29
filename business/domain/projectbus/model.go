package projectbus

import (
	"time"

	"github.com/google/uuid"
)

// Project represents a project within an organization.
type Project struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Name          string
	Color         string
	RetentionDays *int
	DateCreated   time.Time
	DateUpdated   time.Time
}

// NewProject contains information needed to create a new project.
type NewProject struct {
	OrgID uuid.UUID
	Name  string
	Color string
}

// UpdateProject contains information needed to update a project.
type UpdateProject struct {
	Name          *string
	Color         *string
	RetentionDays **int
}
