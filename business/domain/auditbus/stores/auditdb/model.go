package auditdb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jmoiron/sqlx/types"
)

type audit struct {
	ID        uuid.UUID          `db:"id"`
	OrgID     uuid.UUID          `db:"org_id"`
	ObjID     uuid.UUID          `db:"obj_id"`
	ObjDomain string             `db:"obj_domain"`
	ObjName   string             `db:"obj_name"`
	ActorID   uuid.UUID          `db:"actor_id"`
	ActorName string             `db:"actor_name"`
	Action    string             `db:"action"`
	Data      types.NullJSONText `db:"data"`
	Message   string             `db:"message"`
	Timestamp time.Time          `db:"timestamp"`
}

func toDBAudit(bus auditbus.Audit) (audit, error) {
	db := audit{
		ID:        bus.ID,
		OrgID:     bus.OrgID,
		ObjID:     bus.ObjID,
		ObjDomain: bus.ObjDomain.String(),
		ObjName:   bus.ObjName,
		ActorID:   bus.ActorID,
		Action:    bus.Action,
		Data:      types.NullJSONText{JSONText: []byte(bus.Data), Valid: true},
		Message:   bus.Message,
		Timestamp: bus.Timestamp.UTC(),
	}

	return db, nil
}

func toBusAudit(db audit) (auditbus.Audit, error) {
	d, err := domain.Parse(db.ObjDomain)
	if err != nil {
		return auditbus.Audit{}, fmt.Errorf("parse domain: %w", err)
	}

	bus := auditbus.Audit{
		ID:        db.ID,
		OrgID:     db.OrgID,
		ObjID:     db.ObjID,
		ObjDomain: d,
		ObjName:   db.ObjName,
		ActorID:   db.ActorID,
		ActorName: db.ActorName,
		Action:    db.Action,
		Data:      json.RawMessage(db.Data.JSONText),
		Message:   db.Message,
		Timestamp: db.Timestamp.Local(),
	}

	return bus, nil
}

func toBusAudits(dbs []audit) ([]auditbus.Audit, error) {
	audits := make([]auditbus.Audit, len(dbs))

	for i, db := range dbs {
		a, err := toBusAudit(db)
		if err != nil {
			return nil, err
		}

		audits[i] = a
	}

	return audits, nil
}
