package projectdb

import (
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
)

// projectDB is the database representation of a project.
type projectDB struct {
	ID            uuid.UUID `db:"id"`
	OrgID         uuid.UUID `db:"org_id"`
	Name          string    `db:"name"`
	Color         string    `db:"color"`
	RetentionDays *int      `db:"retention_days"`
	DateCreated   time.Time `db:"date_created"`
	DateUpdated   time.Time `db:"date_updated"`
}

func toDBProject(bus projectbus.Project) projectDB {
	return projectDB{
		ID:            bus.ID,
		OrgID:         bus.OrgID,
		Name:          bus.Name,
		Color:         bus.Color,
		RetentionDays: bus.RetentionDays,
		DateCreated:   bus.DateCreated.UTC(),
		DateUpdated:   bus.DateUpdated.UTC(),
	}
}

func toBusProject(db projectDB) projectbus.Project {
	return projectbus.Project{
		ID:            db.ID,
		OrgID:         db.OrgID,
		Name:          db.Name,
		Color:         db.Color,
		RetentionDays: db.RetentionDays,
		DateCreated:   db.DateCreated.In(time.Local),
		DateUpdated:   db.DateUpdated.In(time.Local),
	}
}

func toBusProjects(dbs []projectDB) []projectbus.Project {
	projects := make([]projectbus.Project, len(dbs))
	for i, db := range dbs {
		projects[i] = toBusProject(db)
	}
	return projects
}
