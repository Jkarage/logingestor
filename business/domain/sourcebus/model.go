// Package sourcebus provides business access to the source domain — tenant
// infrastructure-log ingestion endpoints and their scoped ingest keys.
package sourcebus

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("source not found")
	ErrDuplicateName = errors.New("source name already exists in org")
	ErrInvalidKind   = errors.New("invalid source kind")
)

// validKinds is the set of collector kinds a source may declare.
var validKinds = map[string]struct{}{
	"otel":      {},
	"syslog":    {},
	"fluentbit": {},
	"vector":    {},
	"k8s":       {},
	"http":      {},
}

// ValidKind reports whether kind is a recognized collector kind.
func ValidKind(kind string) bool {
	_, ok := validKinds[kind]
	return ok
}

// Default ingest controls applied when a source is created.
const (
	DefaultRateLimitPerSec = 500
	DefaultRateLimitBurst  = 1000
	DefaultSampleDebugInfo = 1.0
)

// Source is an infrastructure-log ingestion endpoint scoped to a project.
type Source struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	ProjectID       uuid.UUID
	Kind            string
	Name            string
	KeyPrefix       string
	KeyHash         string
	IsActive        bool
	LastSeenAt      *time.Time
	RateLimitPerSec int
	RateLimitBurst  int
	SampleDebugInfo float64
	CreatedAt       time.Time

	// ExpiresAt is when the ingest key stops being accepted. Nil means it never
	// expires. Revocation is IsActive = false; expiry is the automatic
	// counterpart so a leaked key lapses without an operator intervening.
	ExpiresAt *time.Time
}

// Expired reports whether the key has passed its expiry as of now.
func (s Source) Expired(now time.Time) bool {
	return s.ExpiresAt != nil && now.After(*s.ExpiresAt)
}

// NewSource contains the data needed to create a source.
type NewSource struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	Kind      string
	Name      string

	// ExpiresAt optionally bounds the lifetime of the generated key.
	ExpiresAt *time.Time
}
