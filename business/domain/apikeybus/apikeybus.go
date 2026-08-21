// Package apikeybus manages read-only API keys for the public query API.
//
// These are deliberately a different scheme from the ingest keys in sourcebus:
// an ingest key writes and cannot read, an API key reads and cannot write, and
// the prefix says which is which before any lookup happens. A leaked key can
// therefore only do the one thing its scheme allows.
package apikeybus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// KeyScheme is the prefix every API key carries.
const KeyScheme = "ls_api_live_"

// keyRandomBytes is the entropy of the random portion of the key (256-bit).
const keyRandomBytes = 32

// MaxNameLen bounds the human label on a key.
const MaxNameLen = 120

// Set of error variables for CRUD operations.
var (
	ErrNotFound          = errors.New("api key not found")
	ErrNameRequired      = errors.New("name is required")
	ErrNameTooLong       = fmt.Errorf("name must be at most %d characters", MaxNameLen)
	ErrExpiryPast        = errors.New("expiresAt must be in the future")
	ErrRateLimitNegative = errors.New("rate limits must be zero (the default) or positive")
)

// APIKey is a read-only credential for the query API. The raw key is never
// stored: only its hash, and a prefix short enough to recognise it in a list.
type APIKey struct {
	ID    uuid.UUID
	OrgID uuid.UUID

	// ProjectID pins the key to one project. Nil means every project in the org,
	// which is what a cross-project export script needs.
	ProjectID *uuid.UUID

	Name      string
	KeyPrefix string
	KeyHash   string

	// RateLimitPerMin and RateLimitBurst bound the query API for this key. Zero
	// means the service default, so a key created before limits existed behaves
	// like every other one.
	RateLimitPerMin int
	RateLimitBurst  int

	IsActive    bool
	CreatedBy   *uuid.UUID
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	DateCreated time.Time
}

// Expired reports whether the key has passed its expiry as of now.
func (k APIKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}

// NewAPIKey is the data needed to mint a key.
type NewAPIKey struct {
	ProjectID *uuid.UUID
	Name      string
	ExpiresAt *time.Time

	// RateLimitPerMin and RateLimitBurst override the service default for this
	// key. Zero leaves it on the default.
	RateLimitPerMin int
	RateLimitBurst  int
}

// GenerateKey mints a key. It returns the raw key — shown to the caller exactly
// once — the hash to persist, and a display prefix.
func GenerateKey() (raw string, keyHash string, keyPrefix string, err error) {
	buf := make([]byte, keyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate key: %w", err)
	}

	randomHex := hex.EncodeToString(buf)
	raw = KeyScheme + randomHex

	return raw, HashKey(raw), KeyScheme + randomHex[:6], nil
}

// HashKey returns the hex-encoded SHA-256 of a raw key.
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HasKeyScheme reports whether raw looks like an API key.
func HasKeyScheme(raw string) bool {
	return strings.HasPrefix(raw, KeyScheme)
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	Create(ctx context.Context, k APIKey) error
	Delete(ctx context.Context, id uuid.UUID) error
	QueryByID(ctx context.Context, id uuid.UUID) (APIKey, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]APIKey, error)
	QueryByKeyHash(ctx context.Context, keyHash string) (APIKey, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID, when time.Time) error
}

// Business manages the set of APIs for API keys.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs an API key business API.
func NewBusiness(log *logger.Logger, storer Storer) *Business {
	return &Business{log: log, storer: storer}
}

// Create mints a key and returns it alongside the raw secret, which the caller
// must surface once and never store.
func (b *Business) Create(ctx context.Context, orgID uuid.UUID, actorID uuid.UUID, nk NewAPIKey) (APIKey, string, error) {
	name := strings.TrimSpace(nk.Name)
	switch {
	case name == "":
		return APIKey{}, "", ErrNameRequired
	case len(name) > MaxNameLen:
		return APIKey{}, "", ErrNameTooLong
	}

	if nk.ExpiresAt != nil && !nk.ExpiresAt.After(time.Now()) {
		return APIKey{}, "", ErrExpiryPast
	}

	if nk.RateLimitPerMin < 0 || nk.RateLimitBurst < 0 {
		return APIKey{}, "", ErrRateLimitNegative
	}

	raw, keyHash, keyPrefix, err := GenerateKey()
	if err != nil {
		return APIKey{}, "", err
	}

	creator := actorID

	k := APIKey{
		ID:              uuid.New(),
		OrgID:           orgID,
		ProjectID:       nk.ProjectID,
		Name:            name,
		KeyPrefix:       keyPrefix,
		KeyHash:         keyHash,
		IsActive:        true,
		CreatedBy:       &creator,
		ExpiresAt:       nk.ExpiresAt,
		RateLimitPerMin: nk.RateLimitPerMin,
		RateLimitBurst:  nk.RateLimitBurst,
		DateCreated:     time.Now(),
	}

	if err := b.storer.Create(ctx, k); err != nil {
		return APIKey{}, "", fmt.Errorf("create: %w", err)
	}

	return k, raw, nil
}

// Revoke deletes a key, which stops it authenticating immediately. There is no
// disable-and-keep: a key nobody can use is a key nobody should be looking at.
func (b *Business) Revoke(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// QueryByID returns one key.
func (b *Business) QueryByID(ctx context.Context, id uuid.UUID) (APIKey, error) {
	k, err := b.storer.QueryByID(ctx, id)
	if err != nil {
		return APIKey{}, fmt.Errorf("querybyid: %w", err)
	}

	return k, nil
}

// QueryByOrg lists an org's keys, newest first.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]APIKey, error) {
	keys, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querybyorg: %w", err)
	}

	return keys, nil
}

// Authenticate resolves a raw key to its record.
//
// It reports one error for every failure mode — unknown, revoked, expired — so
// the caller cannot use the response to learn whether a key exists. Expiry is
// the exception the caller may want to distinguish, so it is returned as a
// separate sentinel.
func (b *Business) Authenticate(ctx context.Context, raw string) (APIKey, error) {
	if !HasKeyScheme(raw) {
		return APIKey{}, ErrNotFound
	}

	k, err := b.storer.QueryByKeyHash(ctx, HashKey(raw))
	if err != nil {
		return APIKey{}, ErrNotFound
	}

	if !k.IsActive {
		return APIKey{}, ErrNotFound
	}

	if k.Expired(time.Now()) {
		return APIKey{}, ErrKeyExpired
	}

	return k, nil
}

// ErrKeyExpired is returned when a key is recognised but past its expiry, so a
// script can be told to rotate rather than that its key is wrong.
var ErrKeyExpired = errors.New("api key has expired")

// TouchLastUsed records that a key was used, throttled by the caller. It is
// best-effort: failing to record use must never fail the request it describes.
func (b *Business) TouchLastUsed(ctx context.Context, id uuid.UUID, when time.Time) {
	if err := b.storer.TouchLastUsed(ctx, id, when); err != nil {
		b.log.Error(ctx, "apikey: touch last used", "id", id, "err", err)
	}
}
