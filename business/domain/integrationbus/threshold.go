package integrationbus

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ThresholdCounter counts logs matching a rule's query inside a window. It lives
// here as an interface because integrationbus must not depend on logbus: the log
// domain already reaches into alerting through the logalert extension, and a
// direct dependency back would be a cycle.
type ThresholdCounter interface {
	CountMatching(ctx context.Context, projectID uuid.UUID, q Query, from, to time.Time) (int, error)
}

// EvaluateThresholds checks every active threshold rule and fires those whose
// window has enough matches.
//
// It is driven on a timer rather than by ingestion because a threshold is a
// statement about a period, not about a log: "twenty errors in five minutes"
// cannot be decided from the batch that happens to contain the twentieth.
//
// A rule that stays over its threshold does not re-notify on every pass — the
// dedup window in ShouldNotify governs that, exactly as for the per-log rules.
func (b *Business) EvaluateThresholds(ctx context.Context, counter ThresholdCounter) (int, error) {
	rules, err := b.storer.QueryThresholdRules(ctx)
	if err != nil {
		return 0, fmt.Errorf("querythresholdrules: %w", err)
	}

	now := time.Now()
	windowsByOrg := make(map[uuid.UUID][]MaintenanceWindow)

	var fired int

	for _, rule := range rules {
		cond := rule.Condition
		if cond.Type != ConditionThreshold || cond.Query == nil {
			continue
		}

		from := now.Add(-cond.Window())

		matched, err := counter.CountMatching(ctx, rule.ProjectID, *cond.Query, from, now)
		if err != nil {
			b.log.Error(ctx, "threshold: count", "ruleID", rule.ID, "err", err)
			continue
		}

		if !cond.Satisfied(matched) {
			// Back under the threshold: close the open alert so the next breach
			// is reported as new rather than folded into a stale one.
			if err := b.resolveOpen(ctx, rule, now); err != nil {
				b.log.Error(ctx, "threshold: resolve", "ruleID", rule.ID, "err", err)
			}
			continue
		}

		windows, ok := windowsByOrg[rule.OrgID]
		if !ok {
			windows, err = b.storer.QueryActiveMaintenance(ctx, rule.OrgID, now)
			if err != nil {
				b.log.Error(ctx, "threshold: maintenance lookup", "orgID", rule.OrgID, "err", err)
			}
			windowsByOrg[rule.OrgID] = windows
		}

		payload := AlertPayload{
			ProjectName: rule.Name,
			Level:       thresholdLevel(*cond.Query),
			Message: fmt.Sprintf("%s: %d matching logs in the last %s (threshold %s %d)",
				rule.Name, matched, cond.Window(), cond.Comparator, cond.Count),
			Source:    "alerting",
			Timestamp: now,
		}

		if err := b.raise(ctx, rule, payload, matched, windows, now); err != nil {
			b.log.Error(ctx, "threshold: raise", "ruleID", rule.ID, "err", err)
			continue
		}

		fired++
	}

	return fired, nil
}

// resolveOpen closes a threshold rule's open alert, if it has one.
func (b *Business) resolveOpen(ctx context.Context, rule AlertRule, now time.Time) error {
	event, err := b.storer.QueryOpenEvent(ctx, rule.ID, rule.Condition.DedupKey(""))
	if err != nil {
		// Nothing open is the normal case, not a problem.
		return nil
	}

	return b.storer.ResolveEvent(ctx, event.ID, now)
}

// thresholdLevel reports the severity to attach to a threshold notification. A
// query naming levels takes the most severe of them; otherwise the breach itself
// is reported as an error, since crossing a threshold is not informational.
func thresholdLevel(q Query) string {
	best := ""
	for _, l := range q.Levels {
		if severityRank(l) > severityRank(best) {
			best = l
		}
	}
	if best == "" {
		return "ERROR"
	}
	return best
}

// AcknowledgeAlert marks an open alert as owned, which stops re-notification
// without resolving it.
func (b *Business) AcknowledgeAlert(ctx context.Context, eventID, userID uuid.UUID) (AlertEvent, error) {
	if err := b.storer.AcknowledgeEvent(ctx, eventID, userID, time.Now()); err != nil {
		return AlertEvent{}, fmt.Errorf("acknowledgeevent: %w", err)
	}

	event, err := b.storer.QueryEventByID(ctx, eventID)
	if err != nil {
		return AlertEvent{}, fmt.Errorf("queryeventbyid: %w", err)
	}

	return event, nil
}

// ResolveAlert closes an alert, freeing its dedup key.
func (b *Business) ResolveAlert(ctx context.Context, eventID uuid.UUID) (AlertEvent, error) {
	if err := b.storer.ResolveEvent(ctx, eventID, time.Now()); err != nil {
		return AlertEvent{}, fmt.Errorf("resolveevent: %w", err)
	}

	event, err := b.storer.QueryEventByID(ctx, eventID)
	if err != nil {
		return AlertEvent{}, fmt.Errorf("queryeventbyid: %w", err)
	}

	return event, nil
}

// QueryAlertEvent returns one alert event.
func (b *Business) QueryAlertEvent(ctx context.Context, id uuid.UUID) (AlertEvent, error) {
	event, err := b.storer.QueryEventByID(ctx, id)
	if err != nil {
		return AlertEvent{}, fmt.Errorf("queryeventbyid: %w", err)
	}
	return event, nil
}

// QueryAlertHistory returns alert events for an org, newest first.
func (b *Business) QueryAlertHistory(ctx context.Context, f AlertEventFilter) ([]AlertEvent, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	events, err := b.storer.QueryEvents(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("queryevents: %w", err)
	}

	return events, nil
}

// CreateMaintenanceWindow schedules a suppression window.
func (b *Business) CreateMaintenanceWindow(ctx context.Context, orgID, actorID uuid.UUID, projectID *uuid.UUID, reason string, startsAt, endsAt time.Time) (MaintenanceWindow, error) {
	if !endsAt.After(startsAt) {
		return MaintenanceWindow{}, fmt.Errorf("endsAt must be after startsAt")
	}

	w := MaintenanceWindow{
		ID: uuid.New(), OrgID: orgID, ProjectID: projectID, Reason: reason,
		StartsAt: startsAt, EndsAt: endsAt, CreatedBy: &actorID, DateCreated: time.Now(),
	}

	if err := b.storer.CreateMaintenance(ctx, w); err != nil {
		return MaintenanceWindow{}, fmt.Errorf("createmaintenance: %w", err)
	}

	return w, nil
}

// DeleteMaintenanceWindow removes a suppression window.
func (b *Business) DeleteMaintenanceWindow(ctx context.Context, id uuid.UUID) error {
	if err := b.storer.DeleteMaintenance(ctx, id); err != nil {
		return fmt.Errorf("deletemaintenance: %w", err)
	}
	return nil
}

// QueryMaintenanceWindows returns an org's suppression windows.
func (b *Business) QueryMaintenanceWindows(ctx context.Context, orgID uuid.UUID) ([]MaintenanceWindow, error) {
	windows, err := b.storer.QueryMaintenanceByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querymaintenancebyorg: %w", err)
	}
	return windows, nil
}
