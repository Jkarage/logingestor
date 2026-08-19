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

	// ActorIP and ActorUserAgent record where the action came from. Both are
	// empty for actions taken by background workers, which have no request.
	ActorIP        string
	ActorUserAgent string
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
