// Package clienterrorbus provides business access to client error monitoring —
// the browser application's own crashes, grouped into issues.
//
// This is not customer log data. It is our own application reporting on itself,
// which is why it lives apart from logbus: the volume is different, the
// retention is shorter, the privacy rules are stricter (no PII at all), and an
// event is worth nothing on its own — the value is the issue it groups into.
package clienterrorbus

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Set of error variables for CRUD operations.
var (
	ErrNotFound      = errors.New("issue not found")
	ErrNoEvents      = errors.New("batch contained no events")
	ErrTooManyEvents = fmt.Errorf("a batch may carry at most %d events", MaxBatchEvents)
	ErrInvalidStatus = errors.New("status must be unresolved, resolved, or ignored")
)

// Limits on what a single report may carry. They are deliberately small: this
// endpoint is unauthenticated, so every field is a place for someone to put a
// megabyte.
const (
	// MaxBatchEvents caps events per request.
	MaxBatchEvents = 50

	// MaxBodyBytes caps the request body.
	MaxBodyBytes = 100 * 1024

	// MaxMessageLen and MaxStackLen bound the two fields that are large by
	// nature. A stack deeper than this is not more diagnostic.
	MaxMessageLen = 1000
	MaxStackLen   = 8000

	// MaxComponentStackLen bounds the React component stack.
	MaxComponentStackLen = 4000

	// MaxBreadcrumbs and MaxBreadcrumbLen bound the trail of recent actions.
	MaxBreadcrumbs   = 30
	MaxBreadcrumbLen = 300

	// MaxURLLen and MaxUserAgentLen bound the context fields.
	MaxURLLen       = 500
	MaxUserAgentLen = 400

	// MaxReleaseLen bounds the release identifier, which is also a grouping key.
	MaxReleaseLen = 120
)

// Levels an event may report.
const (
	LevelFatal   = "fatal"
	LevelError   = "error"
	LevelWarning = "warning"
)

// Kinds of error the client distinguishes.
const (
	KindUnhandled          = "unhandled"
	KindUnhandledRejection = "unhandledrejection"
	KindReact              = "react"
	KindAPI                = "api"
	KindManual             = "manual"
)

// Issue statuses.
const (
	StatusUnresolved = "unresolved"
	StatusResolved   = "resolved"
	StatusIgnored    = "ignored"
)

var validLevels = map[string]bool{LevelFatal: true, LevelError: true, LevelWarning: true}

var validKinds = map[string]bool{
	KindUnhandled: true, KindUnhandledRejection: true, KindReact: true,
	KindAPI: true, KindManual: true,
}

var validStatuses = map[string]bool{StatusUnresolved: true, StatusResolved: true, StatusIgnored: true}

// ValidLevel reports whether a submitted level is one we accept.
func ValidLevel(s string) bool { return validLevels[s] }

// ValidKind reports whether a submitted kind is one we accept.
func ValidKind(s string) bool { return validKinds[s] }

// ParseStatus validates a submitted issue status.
func ParseStatus(s string) (string, error) {
	if !validStatuses[s] {
		return "", ErrInvalidStatus
	}

	return s, nil
}

// Breadcrumb is one recent action leading up to an error.
type Breadcrumb struct {
	TS       time.Time
	Category string
	Message  string
}

// APIContext describes the failed request behind an api-kind error.
type APIContext struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Status int    `json:"status,omitempty"`
	Code   string `json:"code,omitempty"`
}

// NewEvent is one submitted error report, before it is scrubbed and stored.
//
// UserID, OrgID and Role arrive as client hints and are overwritten by the
// server from the presented token; the client's copy is never trusted.
type NewEvent struct {
	EventID        uuid.UUID
	OccurredAt     time.Time
	Level          string
	Kind           string
	Name           string
	Message        string
	Stack          string
	ComponentStack string
	Release        string
	Environment    string
	URL            string
	UserAgent      string
	API            *APIContext
	Breadcrumbs    []Breadcrumb
	SampledCount   int
}

// Reporter is who the server decided the report came from, resolved from the
// token rather than from the payload.
type Reporter struct {
	UserID *uuid.UUID
	OrgID  *uuid.UUID
	Role   string
}

// Event is a stored error report.
type Event struct {
	ID             uuid.UUID
	EventID        uuid.UUID
	OrgID          *uuid.UUID
	UserID         *uuid.UUID
	Role           string
	Level          string
	Kind           string
	Name           string
	Message        string
	Stack          string
	ComponentStack string
	Release        string
	Environment    string
	URL            string
	UserAgent      string
	API            *APIContext
	Breadcrumbs    []Breadcrumb
	OccurredAt     time.Time
	ReceivedAt     time.Time
	Fingerprint    string
	IssueID        *uuid.UUID
	SampledCount   int
}

// Issue is a group of events sharing one fingerprint.
type Issue struct {
	ID            uuid.UUID
	OrgID         *uuid.UUID
	Fingerprint   string
	Title         string
	Culprit       string
	Level         string
	Kind          string
	Status        string
	Regressed     bool
	EventCount    int64
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	ResolvedAt    *time.Time
	AssigneeID    *uuid.UUID
	SampleEventID *uuid.UUID

	// Counts of distinct facets, filled by the read path.
	AffectedUsers int
	AffectedOrgs  int
	Releases      []string
}

// UpdateIssue carries the fields a triaging human may change.
type UpdateIssue struct {
	Status *string

	// AssigneeID is a double pointer so unassigning is distinguishable from
	// leaving the assignee alone.
	AssigneeID **uuid.UUID
}

// IssueFilter narrows an issue listing.
type IssueFilter struct {
	// OrgID scopes the listing. Nil with IncludeAnonymous means the anonymous
	// bucket, which only a super admin may read.
	OrgID  *uuid.UUID
	Status string

	// Release, when set, restricts to issues seen on that release.
	Release string

	// Since bounds by last seen.
	Since *time.Time

	// Sort is one of "lastSeen" (default), "count" or "users".
	Sort  string
	Limit int

	// Cursor is the opaque page cursor from a previous response.
	Cursor string

	// AllOrgs lets a super admin read across every org, including anonymous.
	AllOrgs bool
}

// Sort orders for an issue listing.
const (
	SortLastSeen = "lastSeen"
	SortCount    = "count"
	SortUsers    = "users"
)

// Stats are the dashboard tiles for a window.
type Stats struct {
	From          time.Time
	To            time.Time
	Events        int64
	Issues        int64
	NewIssues     int64
	Unresolved    int64
	AffectedUsers int
}

// Bucket is one point of an issue's count-over-time series.
type Bucket struct {
	TS    time.Time
	Count int64
}
