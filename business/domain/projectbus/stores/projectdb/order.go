package projectdb

import (
	"fmt"

	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/sdk/order"
)

var orderByFields = map[string]string{
	projectbus.OrderByID:    "id",
	projectbus.OrderByName:  "name",
	projectbus.OrderByOrgID: "org_id",
}

func orderByClause(orderBy order.By) (string, error) {
	by, exists := orderByFields[orderBy.Field]
	if !exists {
		return "", fmt.Errorf("field %q does not exist", orderBy.Field)
	}

	return " ORDER BY " + by + " " + orderBy.Direction, nil
}
