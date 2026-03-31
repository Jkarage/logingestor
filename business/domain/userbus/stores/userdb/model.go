package userdb

import (
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb/dbarray"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/role"
)

type userDB struct {
	ID           uuid.UUID      `db:"id"`
	Name         string         `db:"name"`
	Email        string         `db:"email"`
	Roles        dbarray.String `db:"roles"`
	PasswordHash []byte         `db:"password_hash"`
	Enabled      bool           `db:"enabled"`
	DateCreated  time.Time      `db:"date_created"`
	DateUpdated  time.Time      `db:"date_updated"`
}

func toDBUser(bus userbus.User) userDB {
	return userDB{
		ID:           bus.ID,
		Name:         bus.Name.String(),
		Email:        bus.Email.Address,
		Roles:        role.ParseToString(bus.Roles),
		PasswordHash: bus.PasswordHash,
		Enabled:      bus.Enabled,
		DateCreated:  bus.DateCreated.UTC(),
		DateUpdated:  bus.DateUpdated.UTC(),
	}
}

func toBusUser(db userDB) (userbus.User, error) {
	addr := mail.Address{
		Address: db.Email,
	}

	roles, err := role.ParseMany(db.Roles)
	if err != nil {
		return userbus.User{}, fmt.Errorf("parse: %w", err)
	}

	nme, err := name.Parse(db.Name)
	if err != nil {
		return userbus.User{}, fmt.Errorf("parse name: %w", err)
	}

	bus := userbus.User{
		ID:           db.ID,
		Name:         nme,
		Email:        addr,
		Roles:        roles,
		PasswordHash: db.PasswordHash,
		Enabled:      db.Enabled,
		DateCreated:  db.DateCreated.In(time.Local),
		DateUpdated:  db.DateUpdated.In(time.Local),
	}

	return bus, nil
}

func toBusUsers(dbs []userDB) ([]userbus.User, error) {
	bus := make([]userbus.User, len(dbs))

	for i, db := range dbs {
		var err error
		bus[i], err = toBusUser(db)
		if err != nil {
			return nil, err
		}
	}

	return bus, nil
}
