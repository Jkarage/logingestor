package orgbus

import (
	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/name"
)

// QueryFilter holds the available fields a query can be filtered on.
// We are using pointer semantics because the With API mutates the value.
type QueryFilter struct {
	ID      *uuid.UUID
	Name    *name.Name
	Slug    *string
	Enabled *bool
}
