package orgbus

import "github.com/jkarage/logingestor/business/sdk/order"

// DefaultOrderBy represents the default way we sort.
var DefaultOrderBy = order.NewBy(OrderByID, order.ASC)

// Set of fields that the results can be ordered by.
const (
	OrderByID      = "a"
	OrderByName    = "b"
	OrderBySlug    = "c"
	OrderByEnabled = "d"
)
