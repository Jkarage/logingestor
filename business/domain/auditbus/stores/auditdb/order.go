package auditdb

import (
	"fmt"

	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/sdk/order"
)

var orderByFields = map[string]string{
	auditbus.OrderByObjID:     "obj_id",
	auditbus.OrderByObjDomain: "obj_domain",
	auditbus.OrderByObjName:   "obj_name",
	auditbus.OrderByActorID:   "actor_id",
	auditbus.OrderByAction:    "action",
}

func orderByClause(orderBy order.By) (string, error) {
	by, exists := orderByFields[orderBy.Field]
	if !exists {
		return "", fmt.Errorf("field %q does not exist", orderBy.Field)
	}

	return " ORDER BY " + by + " " + orderBy.Direction, nil
}
