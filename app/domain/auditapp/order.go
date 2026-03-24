package auditapp

import "github.com/jkarage/logingestor/business/domain/auditbus"

var orderByFields = map[string]string{
	"obj_id":     auditbus.OrderByObjID,
	"obj_domain": auditbus.OrderByObjDomain,
	"obj_name":   auditbus.OrderByObjName,
	"actor_id":   auditbus.OrderByActorID,
	"action":     auditbus.OrderByAction,
}
