package orgapp

import "github.com/jkarage/logingestor/business/domain/orgbus"

var orderByFields = map[string]string{
	"id":      orgbus.OrderByID,
	"name":    orgbus.OrderByName,
	"slug":    orgbus.OrderBySlug,
	"enabled": orgbus.OrderByEnabled,
}
