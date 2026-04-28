package auditapp

import (
	"encoding/json"
	"time"

	"github.com/jkarage/logingestor/business/domain/auditbus"
)

// Audit represents information about an individual audit record.
type Audit struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"orgId"`
	ObjID     string          `json:"targetId"`
	ObjDomain string          `json:"targetType"`
	ObjName   string          `json:"targetName"`
	ActorID   string          `json:"actorId"`
	ActorName string          `json:"actorName"`
	Action    string          `json:"action"`
	Data      json.RawMessage `json:"meta"`
	Message   string          `json:"message"`
	Timestamp string          `json:"createdAt"`
}

// Encode implements the encoder interface.
func (app Audit) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppAudit(bus auditbus.Audit) Audit {
	meta := bus.Data
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}

	return Audit{
		ID:        bus.ID.String(),
		OrgID:     bus.OrgID.String(),
		ObjID:     bus.ObjID.String(),
		ObjDomain: bus.ObjDomain.String(),
		ObjName:   bus.ObjName.String(),
		ActorID:   bus.ActorID.String(),
		ActorName: bus.ActorName,
		Action:    bus.Action,
		Data:      meta,
		Message:   bus.Message,
		Timestamp: bus.Timestamp.Format(time.RFC3339),
	}
}

func toAppAudits(audits []auditbus.Audit) []Audit {
	app := make([]Audit, len(audits))
	for i, adt := range audits {
		app[i] = toAppAudit(adt)
	}

	return app
}
