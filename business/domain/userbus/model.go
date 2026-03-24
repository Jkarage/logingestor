package userbus

import (
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/password"
	"github.com/jkarage/logingestor/business/types/role"
)

// User represents information about an individual user.
type User struct {
	ID           uuid.UUID
	Name         name.Name
	Email        mail.Address
	Roles        []role.Role
	PasswordHash []byte
	Department   name.Null
	Enabled      bool
	DateCreated  time.Time
	DateUpdated  time.Time
}

// NewUser contains information needed to create a new user.
type NewUser struct {
	Name       name.Name
	Email      mail.Address
	Roles      []role.Role
	Department name.Null
	Password   password.Password
}

// UpdateUser contains information needed to update a user.
type UpdateUser struct {
	Name       *name.Name
	Email      *mail.Address
	Roles      []role.Role
	Department *name.Null
	Password   *password.Password
	Enabled    *bool
}
