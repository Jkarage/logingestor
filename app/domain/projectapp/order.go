package projectapp

import "github.com/jkarage/logingestor/business/domain/projectbus"

var orderByFields = map[string]string{
	"id":    projectbus.OrderByID,
	"name":  projectbus.OrderByName,
	"orgId": projectbus.OrderByOrgID,
}
