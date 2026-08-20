// Package annotationbus manages notes anchored to a moment in a project's logs.
//
// An annotation is either attached to one log line or to a point in time — "we
// deployed 4.12 here" — and both carry a timestamp, so the log view and a chart
// overlay read from the same place.
package annotationbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound     = errors.New("annotation not found")
	ErrBodyRequired = errors.New("body is required")
	ErrBodyTooLong  = fmt.Errorf("body must be at most %d characters", MaxBodyLen)
)

// MaxBodyLen bounds an annotation's text. It is a note, not a document.
const MaxBodyLen = 4000

// DefaultLimit and MaxLimit bound a listing.
const (
	DefaultLimit = 200
	MaxLimit     = 1000
)

// Annotation is a note anchored in a project's timeline.
type Annotation struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID

	// LogID is set when the note is attached to a specific log line. A nil LogID
	// is a timeline marker.
	LogID *uuid.UUID

	// TS is where the note sits on the timeline. For a log annotation it is the
	// log's own timestamp, so the note and the line it describes cannot drift
	// apart in a chart.
	TS          time.Time
	Body        string
	CreatedBy   uuid.UUID
	DateCreated time.Time
	DateUpdated time.Time
}

// NewAnnotation is the data needed to create one.
type NewAnnotation struct {
	ProjectID uuid.UUID
	LogID     *uuid.UUID
	TS        time.Time
	Body      string
}

// UpdateAnnotation carries the fields that may change.
type UpdateAnnotation struct {
	Body *string
}

// Filter narrows a listing.
type Filter struct {
	ProjectIDs []uuid.UUID
	LogID      *uuid.UUID
	From       *time.Time
	To         *time.Time
	Limit      int
}

// Storer declares the persistence behavior this package needs.
type Storer interface {
	Create(ctx context.Context, a Annotation) error
	Update(ctx context.Context, a Annotation) error
	Delete(ctx context.Context, id uuid.UUID) error
	QueryByID(ctx context.Context, id uuid.UUID) (Annotation, error)
	Query(ctx context.Context, f Filter) ([]Annotation, error)
}

// Business manages the set of APIs for annotations.
type Business struct {
	log    *logger.Logger
	storer Storer
}

// NewBusiness constructs an annotation business API.
func NewBusiness(log *logger.Logger, storer Storer) *Business {
	return &Business{log: log, storer: storer}
}

// validateBody checks and trims submitted text.
func validateBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	switch {
	case body == "":
		return "", ErrBodyRequired
	case len(body) > MaxBodyLen:
		return "", ErrBodyTooLong
	}

	return body, nil
}

// Create stores a new annotation owned by actorID.
func (b *Business) Create(ctx context.Context, orgID, actorID uuid.UUID, na NewAnnotation) (Annotation, error) {
	body, err := validateBody(na.Body)
	if err != nil {
		return Annotation{}, err
	}

	ts := na.TS
	if ts.IsZero() {
		ts = time.Now()
	}

	now := time.Now()

	a := Annotation{
		ID:          uuid.New(),
		OrgID:       orgID,
		ProjectID:   na.ProjectID,
		LogID:       na.LogID,
		TS:          ts.UTC(),
		Body:        body,
		CreatedBy:   actorID,
		DateCreated: now,
		DateUpdated: now,
	}

	if err := b.storer.Create(ctx, a); err != nil {
		return Annotation{}, fmt.Errorf("create: %w", err)
	}

	return a, nil
}

// Update applies changes to an existing annotation. Its anchor is immutable:
// moving a note to a different log or moment would silently rewrite history a
// teammate already read, and creating a new one says the same thing honestly.
func (b *Business) Update(ctx context.Context, a Annotation, ua UpdateAnnotation) (Annotation, error) {
	if ua.Body != nil {
		body, err := validateBody(*ua.Body)
		if err != nil {
			return Annotation{}, err
		}
		a.Body = body
	}

	a.DateUpdated = time.Now()

	if err := b.storer.Update(ctx, a); err != nil {
		return Annotation{}, fmt.Errorf("update: %w", err)
	}

	return a, nil
}

// Delete removes an annotation.
func (b *Business) Delete(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// QueryByID returns one annotation.
func (b *Business) QueryByID(ctx context.Context, id uuid.UUID) (Annotation, error) {
	a, err := b.storer.QueryByID(ctx, id)
	if err != nil {
		return Annotation{}, fmt.Errorf("querybyid: %w", err)
	}

	return a, nil
}

// Query lists annotations matching the filter, newest first.
//
// Project scoping is the caller's responsibility to set: the filter takes the
// set of projects the caller may read, and an empty set returns nothing rather
// than everything.
func (b *Business) Query(ctx context.Context, f Filter) ([]Annotation, error) {
	if len(f.ProjectIDs) == 0 {
		return []Annotation{}, nil
	}

	if f.Limit <= 0 || f.Limit > MaxLimit {
		f.Limit = DefaultLimit
	}

	out, err := b.storer.Query(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	return out, nil
}

// CanModify reports whether a caller may edit or delete an annotation. Its
// author always may; an org admin may too, so a team is not stuck with a
// departed colleague's note.
func CanModify(a Annotation, userID uuid.UUID, orgAdmin bool) bool {
	return a.CreatedBy == userID || orgAdmin
}
