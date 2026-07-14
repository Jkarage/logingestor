package sourcebus

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Storer interface declares the behavior this package needs to persist and
// retrieve data.
type Storer interface {
	Create(ctx context.Context, source Source) error
	Update(ctx context.Context, source Source) error
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Source, error)
	QueryByID(ctx context.Context, id uuid.UUID) (Source, error)
	QueryByKeyHash(ctx context.Context, keyHash string) (Source, error)
	TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error
}

// ExtBusiness interface provides support for extensions that wrap extra
// functionality around the core business logic.
type ExtBusiness interface {
	Create(ctx context.Context, actorID uuid.UUID, ns NewSource) (Source, string, error)
	QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Source, error)
	QueryByID(ctx context.Context, id uuid.UUID) (Source, error)
	QueryByKeyHash(ctx context.Context, keyHash string) (Source, error)
	Disable(ctx context.Context, actorID uuid.UUID, source Source) (Source, error)
	RotateKey(ctx context.Context, actorID uuid.UUID, source Source) (Source, string, error)
	TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error
}

// Extension is a function that wraps a new layer of business logic
// around the existing business logic.
type Extension func(ExtBusiness) ExtBusiness

// Business manages the set of APIs for source access.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs a source business API for use.
func NewBusiness(log *logger.Logger, storer Storer, extensions ...Extension) ExtBusiness {
	b := ExtBusiness(&Business{
		log:    log,
		storer: storer,
	})

	for i := len(extensions) - 1; i >= 0; i-- {
		if ext := extensions[i]; ext != nil {
			b = ext(b)
		}
	}

	return b
}

// Create mints a new source and its one-time ingest key. The raw key is
// returned to the caller and never persisted (only its hash + prefix are).
func (b *Business) Create(ctx context.Context, actorID uuid.UUID, ns NewSource) (Source, string, error) {
	if !ValidKind(ns.Kind) {
		return Source{}, "", fmt.Errorf("create: %w: %q", ErrInvalidKind, ns.Kind)
	}

	raw, keyHash, keyPrefix, err := GenerateKey()
	if err != nil {
		return Source{}, "", fmt.Errorf("create: %w", err)
	}

	source := Source{
		ID:              uuid.New(),
		OrgID:           ns.OrgID,
		ProjectID:       ns.ProjectID,
		Kind:            ns.Kind,
		Name:            ns.Name,
		KeyPrefix:       keyPrefix,
		KeyHash:         keyHash,
		IsActive:        true,
		RateLimitPerSec: DefaultRateLimitPerSec,
		RateLimitBurst:  DefaultRateLimitBurst,
		SampleDebugInfo: DefaultSampleDebugInfo,
		CreatedAt:       time.Now(),
	}

	if err := b.storer.Create(ctx, source); err != nil {
		return Source{}, "", fmt.Errorf("create: %w", err)
	}

	return source, raw, nil
}

// QueryByOrg returns all sources belonging to an org.
func (b *Business) QueryByOrg(ctx context.Context, orgID uuid.UUID) ([]Source, error) {
	sources, err := b.storer.QueryByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querybyorg: %w", err)
	}
	return sources, nil
}

// QueryByID finds the source identified by id.
func (b *Business) QueryByID(ctx context.Context, id uuid.UUID) (Source, error) {
	source, err := b.storer.QueryByID(ctx, id)
	if err != nil {
		return Source{}, fmt.Errorf("querybyid: %w", err)
	}
	return source, nil
}

// QueryByKeyHash resolves a source by the SHA-256 hash of its ingest key. Used
// by the ingestion auth path.
func (b *Business) QueryByKeyHash(ctx context.Context, keyHash string) (Source, error) {
	source, err := b.storer.QueryByKeyHash(ctx, keyHash)
	if err != nil {
		return Source{}, fmt.Errorf("querybykeyhash: %w", err)
	}
	return source, nil
}

// Disable soft-disables a source so new writes are rejected while historical
// logs keep their source_id.
func (b *Business) Disable(ctx context.Context, actorID uuid.UUID, source Source) (Source, error) {
	source.IsActive = false
	if err := b.storer.Update(ctx, source); err != nil {
		return Source{}, fmt.Errorf("disable: %w", err)
	}
	return source, nil
}

// RotateKey mints a new ingest key for a source and invalidates the old one by
// replacing its stored hash. Returns the new raw key (shown once).
func (b *Business) RotateKey(ctx context.Context, actorID uuid.UUID, source Source) (Source, string, error) {
	raw, keyHash, keyPrefix, err := GenerateKey()
	if err != nil {
		return Source{}, "", fmt.Errorf("rotatekey: %w", err)
	}

	source.KeyHash = keyHash
	source.KeyPrefix = keyPrefix

	if err := b.storer.Update(ctx, source); err != nil {
		return Source{}, "", fmt.Errorf("rotatekey: %w", err)
	}

	return source, raw, nil
}

// TouchLastSeen records that the source recently ingested data. Callers should
// throttle invocation; this is best-effort observability, not a hot-path write.
func (b *Business) TouchLastSeen(ctx context.Context, id uuid.UUID, when time.Time) error {
	if err := b.storer.TouchLastSeen(ctx, id, when); err != nil {
		return fmt.Errorf("touchlastseen: %w", err)
	}
	return nil
}
