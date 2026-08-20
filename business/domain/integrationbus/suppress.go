package integrationbus

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Alert event states stored in alert_events.state.
const (
	AlertStateFiring       = "firing"
	AlertStateAcknowledged = "acknowledged"
	AlertStateResolved     = "resolved"
)

// DefaultDedupWindow matches the alert_rules.dedup_window_seconds default: after
// notifying, an alert stays quiet this long even while it keeps matching.
const DefaultDedupWindow = 5 * time.Minute

// SuppressReason explains why a firing produced no notification. Every reason is
// recorded rather than silently dropped, so "why didn't I get paged" is always
// answerable.
type SuppressReason string

const (
	// SuppressNone means deliver.
	SuppressNone SuppressReason = ""

	// SuppressDedup means an open alert was notified recently enough that this
	// firing folds into it.
	SuppressDedup SuppressReason = "dedup"

	// SuppressSnoozed means the rule is snoozed until a future time.
	SuppressSnoozed SuppressReason = "snoozed"

	// SuppressMaintenance means a maintenance window covers this moment.
	SuppressMaintenance SuppressReason = "maintenance"

	// SuppressAcknowledged means somebody already took ownership of the open
	// alert, so re-notifying adds noise but no information.
	SuppressAcknowledged SuppressReason = "acknowledged"

	// SuppressInactive means the rule is switched off.
	SuppressInactive SuppressReason = "inactive"
)

// MaintenanceWindow suppresses delivery for an org, or one project within it,
// between two instants.
type MaintenanceWindow struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProjectID   *uuid.UUID
	Reason      string
	StartsAt    time.Time
	EndsAt      time.Time
	CreatedBy   *uuid.UUID
	DateCreated time.Time
}

// Covers reports whether the window suppresses the given project at the given
// instant. A nil ProjectID covers every project in the org.
func (w MaintenanceWindow) Covers(projectID uuid.UUID, at time.Time) bool {
	if w.ProjectID != nil && *w.ProjectID != projectID {
		return false
	}

	// Half-open so back-to-back windows neither overlap nor leave a gap.
	return !at.Before(w.StartsAt) && at.Before(w.EndsAt)
}

// AlertEvent is one alert's lifecycle row. Repeated firings of the same rule and
// dedup key update this row instead of creating another.
type AlertEvent struct {
	ID             uuid.UUID
	RuleID         uuid.UUID
	OrgID          uuid.UUID
	ProjectID      uuid.UUID
	DedupKey       string
	State          string
	Summary        string
	Level          string
	MatchCount     int64
	SampleLogID    *uuid.UUID
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
	LastNotifiedAt *time.Time
	ResolvedAt     *time.Time
	AcknowledgedAt *time.Time
	AcknowledgedBy *uuid.UUID
}

// SuppressInput is everything the decision needs. It is a plain struct so the
// decision stays a pure function and can be tested without a database.
type SuppressInput struct {
	Now time.Time

	RuleActive   bool
	SnoozeUntil  *time.Time
	DedupWindow  time.Duration
	ProjectID    uuid.UUID
	Existing     *AlertEvent
	Maintenances []MaintenanceWindow
}

// ShouldNotify decides whether a firing is delivered, and why not when it is not.
//
// Order matters. Inactive and snoozed are properties of the rule, so they win
// over anything about the alert itself. Maintenance is an operator statement
// about the whole system, so it outranks per-alert state. Acknowledged means
// somebody owns it. Dedup is last, because it is the weakest reason and the one
// most likely to be superseded.
func ShouldNotify(in SuppressInput) (bool, SuppressReason) {
	if !in.RuleActive {
		return false, SuppressInactive
	}

	if in.SnoozeUntil != nil && in.Now.Before(*in.SnoozeUntil) {
		return false, SuppressSnoozed
	}

	for _, w := range in.Maintenances {
		if w.Covers(in.ProjectID, in.Now) {
			return false, SuppressMaintenance
		}
	}

	// No open alert: this is a new one, so it always notifies.
	if in.Existing == nil || in.Existing.State == AlertStateResolved {
		return true, SuppressNone
	}

	if in.Existing.State == AlertStateAcknowledged {
		return false, SuppressAcknowledged
	}

	// Open and unacknowledged. Notify again only once the dedup window has
	// elapsed since the last notification, so a burst becomes one page and a
	// persistent problem still re-pages periodically.
	window := in.DedupWindow
	if window <= 0 {
		window = DefaultDedupWindow
	}

	if in.Existing.LastNotifiedAt == nil {
		return true, SuppressNone
	}

	if in.Now.Sub(*in.Existing.LastNotifiedAt) >= window {
		return true, SuppressNone
	}

	return false, SuppressDedup
}

// ErrEventNotFound is returned when no matching alert event exists.
var ErrEventNotFound = errors.New("alert event not found")

// AlertEventFilter selects alert history.
type AlertEventFilter struct {
	OrgID     uuid.UUID
	ProjectID *uuid.UUID
	RuleID    *uuid.UUID
	State     *string
	Since     *time.Time
	Limit     int
}
