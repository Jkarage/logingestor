package userapp

import "github.com/jkarage/logingestor/business/domain/userbus"

var orderByFields = map[string]string{
	"id":      userbus.OrderByID,
	"name":    userbus.OrderByName,
	"email":   userbus.OrderByEmail,
	"roles":   userbus.OrderByRoles,
	"enabled": userbus.OrderByEnabled,
}
