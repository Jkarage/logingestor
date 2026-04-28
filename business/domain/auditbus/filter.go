package auditbus

import (
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/domain"
	"github.com/jkarage/logingestor/business/types/name"
)

// QueryFilter holds the available fields a query can be filtered on.
// We are using pointer semantics because the With API mutates the value.
type QueryFilter struct {
	OrgID     *uuid.UUID
	ObjID     *uuid.UUID
	ObjDomain *domain.Domain
	ObjName   *name.Name
	ActorID   *uuid.UUID
	Action    *string
	Since     *time.Time
	Until     *time.Time
}
