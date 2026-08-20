package viewbus

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Permalink kinds.
const (
	// PermalinkLog points at one log line in one project.
	PermalinkLog = "log"

	// PermalinkQuery points at a frozen query — the filters and the time range
	// as they were when the link was made, so a shared link keeps showing the
	// same window rather than drifting with "last 15 minutes".
	PermalinkQuery = "query"
)

// slugBytes is the entropy behind a permalink slug. A slug is a capability: it
// is unguessable so the URL can be handed around inside a team, but it is not an
// authorization — reading a permalink still requires access to the org.
const slugBytes = 12

// Errors returned when creating or resolving a permalink.
var (
	ErrPermalinkKind    = fmt.Errorf("permalink kind must be %q or %q", PermalinkLog, PermalinkQuery)
	ErrPermalinkLogID   = errors.New("a log permalink needs a projectId and a logId")
	ErrPermalinkQueryID = errors.New("a query permalink must not carry a logId")
)

// Permalink is a short, stable pointer to a log or a query.
type Permalink struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProjectID   *uuid.UUID
	Slug        string
	Kind        string
	LogID       *uuid.UUID
	Query       json.RawMessage
	CreatedBy   uuid.UUID
	DateCreated time.Time
}

// NewPermalink is the data needed to create one.
type NewPermalink struct {
	Kind      string
	ProjectID *uuid.UUID
	LogID     *uuid.UUID
	Query     json.RawMessage
}

// GenerateSlug mints an unguessable, URL-safe slug.
func GenerateSlug() (string, error) {
	buf := make([]byte, slugBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate slug: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validatePermalink checks a submitted permalink and normalises its query.
func validatePermalink(np NewPermalink) (NewPermalink, error) {
	switch np.Kind {
	case PermalinkLog:
		if np.LogID == nil || np.ProjectID == nil {
			return np, ErrPermalinkLogID
		}

	case PermalinkQuery:
		if np.LogID != nil {
			return np, ErrPermalinkQueryID
		}

	default:
		return np, ErrPermalinkKind
	}

	query, err := validateDefinition(np.Query, "{}", false)
	if err != nil {
		return np, err
	}
	np.Query = query

	return np, nil
}

// CreatePermalink stores a new permalink owned by actorID.
//
// A slug collision is astronomically unlikely at 96 bits, but the unique index
// is the authority, so a rejected insert is retried rather than surfaced.
func (b *Business) CreatePermalink(ctx context.Context, orgID, actorID uuid.UUID, np NewPermalink) (Permalink, error) {
	np, err := validatePermalink(np)
	if err != nil {
		return Permalink{}, err
	}

	p := Permalink{
		ID:          uuid.New(),
		OrgID:       orgID,
		ProjectID:   np.ProjectID,
		Kind:        np.Kind,
		LogID:       np.LogID,
		Query:       np.Query,
		CreatedBy:   actorID,
		DateCreated: time.Now(),
	}

	for attempt := 0; attempt < 3; attempt++ {
		slug, err := GenerateSlug()
		if err != nil {
			return Permalink{}, err
		}
		p.Slug = slug

		err = b.storer.CreatePermalink(ctx, p)
		switch {
		case err == nil:
			return p, nil
		case errors.Is(err, ErrSlugTaken):
			continue
		default:
			return Permalink{}, fmt.Errorf("createpermalink: %w", err)
		}
	}

	return Permalink{}, errors.New("createpermalink: could not allocate a unique slug")
}

// QueryPermalinkBySlug resolves a slug.
func (b *Business) QueryPermalinkBySlug(ctx context.Context, orgID uuid.UUID, slug string) (Permalink, error) {
	p, err := b.storer.QueryPermalinkBySlug(ctx, slug)
	if err != nil {
		return Permalink{}, fmt.Errorf("querypermalinkbyslug: %w", err)
	}

	// Scoped to the org in the path, so a slug from another tenant reads as
	// missing rather than as forbidden.
	if p.OrgID != orgID {
		return Permalink{}, ErrNotFound
	}

	return p, nil
}

// QueryPermalinksByOrg lists an org's permalinks, newest first.
func (b *Business) QueryPermalinksByOrg(ctx context.Context, orgID uuid.UUID) ([]Permalink, error) {
	links, err := b.storer.QueryPermalinksByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querypermalinksbyorg: %w", err)
	}

	return links, nil
}

// DeletePermalink removes a permalink, revoking the link.
func (b *Business) DeletePermalink(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.DeletePermalink(ctx, id); err != nil {
		return fmt.Errorf("deletepermalink: %w", err)
	}

	return nil
}
