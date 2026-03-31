// Package invitationbus provides business access to the org invitation domain.
package invitationbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/delegate"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("invitation not found")
	ErrAlreadyUsed   = errors.New("invitation has already been accepted")
	ErrExpired       = errors.New("invitation has expired")
)

// Storer interface declares the behavior this package needs to persist and
// retrieve data.
type Storer interface {
	NewWithTx(tx sqldb.CommitRollbacker) (Storer, error)

	Create(ctx context.Context, inv Invitation) error
	QueryByID(ctx context.Context, invID uuid.UUID) (Invitation, error)
	QueryByToken(ctx context.Context, token string) (Invitation, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Invitation, error)
	Delete(ctx context.Context, invID uuid.UUID) error
	MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error)

	Create(ctx context.Context, actorID uuid.UUID, ni NewInvitation) (Invitation, error)
	Revoke(ctx context.Context, actorID uuid.UUID, invID uuid.UUID) error
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Invitation, error)
	QueryByToken(ctx context.Context, token string) (Invitation, error)
	QueryByID(ctx context.Context, invID uuid.UUID) (Invitation, error)
	MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error
}

// Extension is a function that wraps a new layer of business logic
// around the existing business logic.
type Extension func(ExtBusiness) ExtBusiness

// Business manages the set of APIs for invitation access.
type Business struct {
	log        *logger.Logger
	storer     Storer
	delegate   *delegate.Delegate
	extensions []Extension
}

// NewBusiness constructs an invitation business API for use.
func NewBusiness(log *logger.Logger, delegate *delegate.Delegate, storer Storer, extensions ...Extension) ExtBusiness {
	b := ExtBusiness(&Business{
		log:        log,
		delegate:   delegate,
		storer:     storer,
		extensions: extensions,
	})

	for i := len(extensions) - 1; i >= 0; i-- {
		ext := extensions[i]
		if ext != nil {
			b = ext(b)
		}
	}

	return b
}

// NewWithTx constructs a new business value that will use the
// specified transaction in any store related calls.
func (b *Business) NewWithTx(tx sqldb.CommitRollbacker) (ExtBusiness, error) {
	storer, err := b.storer.NewWithTx(tx)
	if err != nil {
		return nil, err
	}

	return NewBusiness(b.log, b.delegate, storer, b.extensions...), nil
}

// Create stores a new invitation row. The token and ExpiresAt are already
// set on NewInvitation by the app layer (which holds the auth dependency).
func (b *Business) Create(ctx context.Context, actorID uuid.UUID, ni NewInvitation) (Invitation, error) {
	inv := Invitation{
		ID:         uuid.New(),
		OrgID:      ni.OrgID,
		Email:      ni.Email,
		Role:       ni.Role,
		Token:      ni.Token,
		InvitedBy:  actorID,
		ProjectIDs: ni.ProjectIDs,
		ExpiresAt:  ni.ExpiresAt,
		CreatedAt:  time.Now(),
	}

	if err := b.storer.Create(ctx, inv); err != nil {
		return Invitation{}, fmt.Errorf("create: %w", err)
	}

	return inv, nil
}

// Revoke deletes a pending invitation. It is an error to revoke an already
// accepted invitation.
func (b *Business) Revoke(ctx context.Context, actorID uuid.UUID, invID uuid.UUID) error {
	inv, err := b.storer.QueryByID(ctx, invID)
	if err != nil {
		return fmt.Errorf("querybyid: %w", err)
	}

	if inv.AcceptedAt != nil {
		return ErrAlreadyUsed
	}

	if err := b.storer.Delete(ctx, invID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// QueryByOrg returns all invitations (pending and accepted) for an org.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Invitation, error) {
	invs, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querybyorg: %w", err)
	}
	return invs, nil
}

// QueryByToken retrieves an invitation by its signed token string.
func (b *Business) QueryByToken(ctx context.Context, token string) (Invitation, error) {
	inv, err := b.storer.QueryByToken(ctx, token)
	if err != nil {
		return Invitation{}, fmt.Errorf("querybytoken: %w", err)
	}
	return inv, nil
}

// QueryByID retrieves an invitation by its ID.
func (b *Business) QueryByID(ctx context.Context, invID uuid.UUID) (Invitation, error) {
	inv, err := b.storer.QueryByID(ctx, invID)
	if err != nil {
		return Invitation{}, fmt.Errorf("querybyid: %w", err)
	}
	return inv, nil
}

// MarkAccepted stamps the invitation row with the acceptance time.
func (b *Business) MarkAccepted(ctx context.Context, invID uuid.UUID, acceptedAt time.Time) error {
	if err := b.storer.MarkAccepted(ctx, invID, acceptedAt); err != nil {
		return fmt.Errorf("markaccepted: %w", err)
	}
	return nil
}
