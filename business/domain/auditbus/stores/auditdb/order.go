package auditdb

import (
	"fmt"

	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/sdk/order"
)

var orderByFields = map[string]string{
	auditbus.OrderByObjID:     "a.obj_id",
	auditbus.OrderByObjDomain: "a.obj_domain",
	auditbus.OrderByObjName:   "a.obj_name",
	auditbus.OrderByActorID:   "a.actor_id",
	auditbus.OrderByAction:    "a.action",
	auditbus.OrderByTimestamp: "a.timestamp",
}

func orderByClause(orderBy order.By) (string, error) {
	by, exists := orderByFields[orderBy.Field]
	if !exists {
		return "", fmt.Errorf("field %q does not exist", orderBy.Field)
	}

	return " ORDER BY " + by + " " + orderBy.Direction, nil
}
