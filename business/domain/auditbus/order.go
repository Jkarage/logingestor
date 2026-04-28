package auditbus

import "github.com/jkarage/logingestor/business/sdk/order"

// DefaultOrderBy represents the default way we sort.
var DefaultOrderBy = order.NewBy(OrderByTimestamp, order.DESC)

// Set of fields that the results can be ordered by.
const (
	OrderByObjID     = "a"
	OrderByObjDomain = "b"
	OrderByObjName   = "c"
	OrderByActorID   = "d"
	OrderByAction    = "e"
	OrderByTimestamp = "f"
)
