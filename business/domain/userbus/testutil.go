package userbus

import (
	"context"
	"fmt"
	"math/rand"
	"net/mail"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/types/name"
	"github.com/jkarage/logingestor/business/types/password"
	"github.com/jkarage/logingestor/business/types/role"
)

// TestNewUsers is a helper method for testing.
func TestNewUsers(n int, rle role.Role) []NewUser {
	newUsrs := make([]NewUser, n)

	idx := rand.Intn(10000)
	for i := range n {
		idx++

		nu := NewUser{
			Name:     name.MustParse(fmt.Sprintf("Name%d", idx)),
			Email:    mail.Address{Address: fmt.Sprintf("Email%d@gmail.com", idx)},
			Roles:    []role.Role{rle},
			Password: password.MustParse(fmt.Sprintf("Password%d", idx)),
		}

		newUsrs[i] = nu
	}

	return newUsrs
}

// TestSeedUsers is a helper method for testing.
func TestSeedUsers(ctx context.Context, n int, role role.Role, api ExtBusiness) ([]User, error) {
	newUsrs := TestNewUsers(n, role)

	usrs := make([]User, len(newUsrs))
	for i, nu := range newUsrs {
		usr, err := api.Create(ctx, uuid.UUID{}, nu)
		if err != nil {
			return nil, fmt.Errorf("seeding user: idx: %d : %w", i, err)
		}

		usrs[i] = usr
	}

	return usrs, nil
}
