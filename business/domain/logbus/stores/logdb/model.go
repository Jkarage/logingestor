package logdb

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
)

// jsonMap is a map[string]any that reads/writes as JSONB.
type jsonMap map[string]any

func (m jsonMap) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

func (m *jsonMap) Scan(src any) error {
	if src == nil {
		*m = make(jsonMap)
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("logdb: cannot scan %T into jsonMap", src)
	}
	return json.Unmarshal(b, m)
}

// logDB is the database representation of a logs row.
type logDB struct {
	ID        uuid.UUID      `db:"id"`
	ProjectID uuid.UUID      `db:"project_id"`
	Level     string         `db:"level"`
	Message   string         `db:"message"`
	Source    string         `db:"source"`
	Ts        time.Time      `db:"ts"`
	Tags      dbarray.String `db:"tags"`
	Meta      jsonMap        `db:"meta"`
}

// statsRow is used for level-count aggregates.
type statsRow struct {
	Level string `db:"level"`
	Count int    `db:"count"`
}

func toDBLog(bus logbus.Log) logDB {
	tags := make(dbarray.String, len(bus.Tags))
	copy(tags, bus.Tags)

	return logDB{
		ID:        bus.ID,
		ProjectID: bus.ProjectID,
		Level:     bus.Level.String(),
		Message:   bus.Message,
		Source:    bus.Source,
		Ts:        bus.Timestamp.UTC(),
		Tags:      tags,
		Meta:      jsonMap(bus.Meta),
	}
}

func toBusLog(db logDB) (logbus.Log, error) {
	lvl, err := logbus.ParseLevel(db.Level)
	if err != nil {
		return logbus.Log{}, fmt.Errorf("parse level: %w", err)
	}

	tags := make([]string, len(db.Tags))
	copy(tags, db.Tags)

	return logbus.Log{
		ID:        db.ID,
		ProjectID: db.ProjectID,
		Level:     lvl,
		Message:   db.Message,
		Source:    db.Source,
		Timestamp: db.Ts.In(time.Local),
		Tags:      tags,
		Meta:      map[string]any(db.Meta),
	}, nil
}

func toBusLogs(dbs []logDB) ([]logbus.Log, error) {
	logs := make([]logbus.Log, len(dbs))
	for i, db := range dbs {
		l, err := toBusLog(db)
		if err != nil {
			return nil, err
		}
		logs[i] = l
	}
	return logs, nil
}
