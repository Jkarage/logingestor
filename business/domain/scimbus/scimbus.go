// Package scimbus manages per-organization SCIM 2.0 provisioning credentials.
//
// A token authenticates an identity provider's provisioning agent and resolves
// it to exactly one organization, so a SCIM request can never touch another
// tenant. Only the SHA-256 hash is persisted.
package scimbus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// ErrNotFound is returned when no token matches.
var ErrNotFound = errors.New("scim token not found")

// keyScheme prefixes every issued token so a leaked string is recognisable in
// logs and secret scanners.
const keyScheme = "ls_scim_"

// Token is an organization's SCIM credential. Raw is only ever populated at
// issue time.
type Token struct {
	OrgID       uuid.UUID
	TokenHash   string
	TokenPrefix string
	DateCreated time.Time
	LastUsedAt  *time.Time
}

// HashToken returns the stored representation of a raw token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HasKeyScheme reports whether raw looks like one of our SCIM tokens.
func HasKeyScheme(raw string) bool {
	return strings.HasPrefix(raw, keyScheme)
}

// GenerateToken mints a raw token plus its stored hash and display prefix.
func GenerateToken() (raw, hash, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("read random: %w", err)
	}

	raw = keyScheme + base64.RawURLEncoding.EncodeToString(buf)

	// Enough to identify a token in a UI without being useful on its own.
	prefix = raw[:len(keyScheme)+8]

	return raw, HashToken(raw), prefix, nil
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	Upsert(ctx context.Context, t Token) error
	QueryByHash(ctx context.Context, hash string) (Token, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) (Token, error)
	Delete(ctx context.Context, orgID uuid.UUID) error
	TouchLastUsed(ctx context.Context, orgID uuid.UUID, when time.Time) error
}

// Business manages the set of APIs for SCIM credentials.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs a SCIM business API.
func NewBusiness(log *logger.Logger, storer Storer) *Business {
	return &Business{log: log, storer: storer}
}

// Issue mints a new token for an org, replacing any existing one. The raw token
// is returned once and cannot be recovered afterwards.
func (b *Business) Issue(ctx context.Context, orgID uuid.UUID) (Token, string, error) {
	raw, hash, prefix, err := GenerateToken()
	if err != nil {
		return Token{}, "", err
	}

	t := Token{
		OrgID:       orgID,
		TokenHash:   hash,
		TokenPrefix: prefix,
		DateCreated: time.Now(),
	}

	if err := b.storer.Upsert(ctx, t); err != nil {
		return Token{}, "", fmt.Errorf("upsert: %w", err)
	}

	return t, raw, nil
}

// QueryByOrg returns an org's token metadata, never the raw token.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) (Token, error) {
	t, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return Token{}, fmt.Errorf("querybyorg: %w", err)
	}
	return t, nil
}

// Authenticate resolves a raw token to the organization it provisions for and
// records the use.
func (b *Business) Authenticate(ctx context.Context, raw string) (uuid.UUID, error) {
	if !HasKeyScheme(raw) {
		return uuid.UUID{}, ErrNotFound
	}

	t, err := b.storer.QueryByHash(ctx, HashToken(raw))
	if err != nil {
		return uuid.UUID{}, ErrNotFound
	}

	// Best effort: a failed timestamp update must not fail the request.
	if err := b.storer.TouchLastUsed(ctx, t.OrgID, time.Now()); err != nil {
		b.log.Error(ctx, "scim: touch last used", "orgID", t.OrgID, "err", err)
	}

	return t.OrgID, nil
}

// Revoke deletes an org's token.
func (b *Business) Revoke(ctx context.Context, orgID uuid.UUID) error {
	if err := b.storer.Delete(ctx, orgID); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
