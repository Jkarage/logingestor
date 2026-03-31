package orgdb

import (
	"fmt"

	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/sdk/order"
)

var orderByFields = map[string]string{
	orgbus.OrderByID:      "id",
	orgbus.OrderByName:    "name",
	orgbus.OrderBySlug:    "slug",
	orgbus.OrderByEnabled: "enabled",
}

func orderByClause(orderBy order.By) (string, error) {
	by, exists := orderByFields[orderBy.Field]
	if !exists {
		return "", fmt.Errorf("field %q does not exist", orderBy.Field)
	}

	return " ORDER BY " + by + " " + orderBy.Direction, nil
}
