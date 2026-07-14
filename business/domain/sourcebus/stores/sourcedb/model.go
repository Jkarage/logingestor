package sourcedb

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
)

// sourceDB is the database representation of a sources row.
type sourceDB struct {
	ID              uuid.UUID    `db:"id"`
	OrgID           uuid.UUID    `db:"org_id"`
	ProjectID       uuid.UUID    `db:"project_id"`
	Kind            string       `db:"kind"`
	Name            string       `db:"name"`
	KeyPrefix       string       `db:"key_prefix"`
	KeyHash         string       `db:"key_hash"`
	IsActive        bool         `db:"is_active"`
	LastSeenAt      sql.NullTime `db:"last_seen_at"`
	RateLimitPerSec int          `db:"rate_limit_per_sec"`
	RateLimitBurst  int          `db:"rate_limit_burst"`
	SampleDebugInfo float64      `db:"sample_debug_info"`
	CreatedAt       time.Time    `db:"created_at"`
}

func toDBSource(bus sourcebus.Source) sourceDB {
	db := sourceDB{
		ID:              bus.ID,
		OrgID:           bus.OrgID,
		ProjectID:       bus.ProjectID,
		Kind:            bus.Kind,
		Name:            bus.Name,
		KeyPrefix:       bus.KeyPrefix,
		KeyHash:         bus.KeyHash,
		IsActive:        bus.IsActive,
		RateLimitPerSec: bus.RateLimitPerSec,
		RateLimitBurst:  bus.RateLimitBurst,
		SampleDebugInfo: bus.SampleDebugInfo,
		CreatedAt:       bus.CreatedAt.UTC(),
	}
	if bus.LastSeenAt != nil {
		db.LastSeenAt = sql.NullTime{Time: bus.LastSeenAt.UTC(), Valid: true}
	}
	return db
}

func toBusSource(db sourceDB) sourcebus.Source {
	src := sourcebus.Source{
		ID:              db.ID,
		OrgID:           db.OrgID,
		ProjectID:       db.ProjectID,
		Kind:            db.Kind,
		Name:            db.Name,
		KeyPrefix:       db.KeyPrefix,
		KeyHash:         db.KeyHash,
		IsActive:        db.IsActive,
		RateLimitPerSec: db.RateLimitPerSec,
		RateLimitBurst:  db.RateLimitBurst,
		SampleDebugInfo: db.SampleDebugInfo,
		CreatedAt:       db.CreatedAt.In(time.Local),
	}
	if db.LastSeenAt.Valid {
		t := db.LastSeenAt.Time.In(time.Local)
		src.LastSeenAt = &t
	}
	return src
}

func toBusSources(dbs []sourceDB) []sourcebus.Source {
	sources := make([]sourcebus.Source, len(dbs))
	for i, db := range dbs {
		sources[i] = toBusSource(db)
	}
	return sources
}
