// Package useraudit provides an extension for userbus that adds
// auditing functionality.
package useraudit

import (
	"context"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/types/domain"
)

// Extension provides a wrapper for audit functionality around the userbus.
type Extension struct {
	bus      userbus.ExtBusiness
	auditBus auditbus.ExtBusiness
}

// NewExtension constructs a new extension that wraps the userbus with audit.
func NewExtension(auditBus auditbus.ExtBusiness) userbus.Extension {
	return func(bus userbus.ExtBusiness) userbus.ExtBusiness {
		return &Extension{
			bus:      bus,
			auditBus: auditBus,
		}
	}
}

// NewWithTx does not apply auditing.
func (ext *Extension) NewWithTx(tx sqldb.CommitRollbacker) (userbus.ExtBusiness, error) {
	return ext.bus.NewWithTx(tx)
}

// Create applies auditing to the user creation process.
func (ext *Extension) Create(ctx context.Context, actorID uuid.UUID, nu userbus.NewUser) (userbus.User, error) {
	usr, err := ext.bus.Create(ctx, actorID, nu)
	if err != nil {
		return userbus.User{}, err
	}

	na := auditbus.NewAudit{
		ObjID:     usr.ID,
		ObjDomain: domain.User,
		ObjName:   usr.Name.String(),
		ActorID:   actorID,
		Action:    "user.created",
		Data:      map[string]any{"name": usr.Name.String(), "email": usr.Email.Address},
		Message:   "user created",
	}

	if _, err := ext.auditBus.Create(ctx, na); err != nil {
		return userbus.User{}, err
	}

	return usr, nil
}

// Update applies auditing to the user update process.
func (ext *Extension) Update(ctx context.Context, actorID uuid.UUID, usr userbus.User, uu userbus.UpdateUser) (userbus.User, error) {
	before := usr
	usr, err := ext.bus.Update(ctx, actorID, usr, uu)
	if err != nil {
		return userbus.User{}, err
	}

	meta := map[string]any{}
	if uu.Name != nil {
		meta["old_name"] = before.Name.String()
		meta["name"] = usr.Name.String()
	}
	if uu.Email != nil {
		meta["old_email"] = before.Email.Address
		meta["email"] = usr.Email.Address
	}
	if uu.Enabled != nil {
		meta["old_enabled"] = before.Enabled
		meta["enabled"] = usr.Enabled
	}

	na := auditbus.NewAudit{
		ObjID:     usr.ID,
		ObjDomain: domain.User,
		ObjName:   usr.Name.String(),
		ActorID:   actorID,
		Action:    "user.updated",
		Data:      meta,
		Message:   "user updated",
	}

	if _, err := ext.auditBus.Create(ctx, na); err != nil {
		return userbus.User{}, err
	}

	return usr, nil
}

// Delete applies auditing to the user deletion process.
func (ext *Extension) Delete(ctx context.Context, actorID uuid.UUID, usr userbus.User) error {
	if err := ext.bus.Delete(ctx, actorID, usr); err != nil {
		return err
	}

	na := auditbus.NewAudit{
		ObjID:     usr.ID,
		ObjDomain: domain.User,
		ObjName:   usr.Name.String(),
		ActorID:   actorID,
		Action:    "user.deleted",
		Data:      map[string]any{"name": usr.Name.String(), "email": usr.Email.Address},
		Message:   "user deleted",
	}

	if _, err := ext.auditBus.Create(ctx, na); err != nil {
		return err
	}

	return nil
}

// Query does not apply auditing.
func (ext *Extension) Query(ctx context.Context, filter userbus.QueryFilter, orderBy order.By, page page.Page) ([]userbus.User, error) {
	return ext.bus.Query(ctx, filter, orderBy, page)
}

// Count does not apply auditing.
func (ext *Extension) Count(ctx context.Context, filter userbus.QueryFilter) (int, error) {
	return ext.bus.Count(ctx, filter)
}

// QueryByID does not apply auditing.
func (ext *Extension) QueryByID(ctx context.Context, userID uuid.UUID) (userbus.User, error) {
	return ext.bus.QueryByID(ctx, userID)
}

// QueryByEmail does not apply auditing.
func (ext *Extension) QueryByEmail(ctx context.Context, email mail.Address) (userbus.User, error) {
	return ext.bus.QueryByEmail(ctx, email)
}

// Authenticate does not apply auditing.
func (ext *Extension) Authenticate(ctx context.Context, email mail.Address, password string) (userbus.User, error) {
	return ext.bus.Authenticate(ctx, email, password)
}

func (ext *Extension) Activate(ctx context.Context, userID uuid.UUID) error {
	return ext.bus.Activate(ctx, userID)
}

func (ext *Extension) StoreVerifyToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error {
	return ext.bus.StoreVerifyToken(ctx, userID, token, expiresAt)
}

func (ext *Extension) ConsumeVerifyToken(ctx context.Context, token string) (uuid.UUID, error) {
	return ext.bus.ConsumeVerifyToken(ctx, token)
}
