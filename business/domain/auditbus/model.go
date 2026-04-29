package auditbus

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/domain"
)

// Audit represents information about an individual audit record.
type Audit struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	ObjID     uuid.UUID
	ObjDomain domain.Domain
	ObjName   string
	ActorID   uuid.UUID
	ActorName string
	Action    string
	Data      json.RawMessage
	Message   string
	Timestamp time.Time
}

// NewAudit represents the information needed to create a new audit record.
type NewAudit struct {
	OrgID     uuid.UUID
	ObjID     uuid.UUID
	ObjDomain domain.Domain
	ObjName   string
	ActorID   uuid.UUID
	Action    string
	Data      any
	Message   string
}
