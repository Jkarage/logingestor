package integrationapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// AlertEvent is one alert's lifecycle as returned by the API.
type AlertEvent struct {
	ID             string  `json:"id"`
	RuleID         string  `json:"ruleId"`
	ProjectID      string  `json:"projectId"`
	DedupKey       string  `json:"dedupKey"`
	State          string  `json:"state"`
	Summary        string  `json:"summary"`
	Level          string  `json:"level"`
	MatchCount     int64   `json:"matchCount"`
	SampleLogID    *string `json:"sampleLogId"`
	FirstSeenAt    string  `json:"firstSeenAt"`
	LastSeenAt     string  `json:"lastSeenAt"`
	LastNotifiedAt *string `json:"lastNotifiedAt"`
	ResolvedAt     *string `json:"resolvedAt"`
	AcknowledgedAt *string `json:"acknowledgedAt"`
	AcknowledgedBy *string `json:"acknowledgedBy"`
}

// Encode implements the encoder interface.
func (app AlertEvent) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// AlertEvents is the history list response.
type AlertEvents struct {
	Alerts []AlertEvent `json:"alerts"`
}

// Encode implements the encoder interface.
func (app AlertEvents) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppAlertEvent(e integrationbus.AlertEvent) AlertEvent {
	out := AlertEvent{
		ID: e.ID.String(), RuleID: e.RuleID.String(), ProjectID: e.ProjectID.String(),
		DedupKey: e.DedupKey, State: e.State, Summary: e.Summary, Level: e.Level,
		MatchCount:  e.MatchCount,
		FirstSeenAt: e.FirstSeenAt.Format(time.RFC3339),
		LastSeenAt:  e.LastSeenAt.Format(time.RFC3339),
	}

	if e.SampleLogID != nil {
		s := e.SampleLogID.String()
		out.SampleLogID = &s
	}
	if e.AcknowledgedBy != nil {
		s := e.AcknowledgedBy.String()
		out.AcknowledgedBy = &s
	}
	for _, f := range []struct {
		src *time.Time
		dst **string
	}{
		{e.LastNotifiedAt, &out.LastNotifiedAt},
		{e.ResolvedAt, &out.ResolvedAt},
		{e.AcknowledgedAt, &out.AcknowledgedAt},
	} {
		if f.src != nil {
			s := f.src.Format(time.RFC3339)
			*f.dst = &s
		}
	}

	return out
}

// queryAlerts returns alert history for an org, newest first.
// GET /v1/orgs/{org_id}/alerts?projectId&state&ruleId&since&limit
func (a *app) queryAlerts(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	q := r.URL.Query()
	filter := integrationbus.AlertEventFilter{OrgID: orgID}

	for _, f := range []struct {
		name string
		dst  **uuid.UUID
	}{{"projectId", &filter.ProjectID}, {"ruleId", &filter.RuleID}} {
		v := q.Get(f.name)
		if v == "" {
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return errs.Errorf(errs.InvalidArgument, "invalid '%s'", f.name)
		}
		*f.dst = &id
	}

	if state := q.Get("state"); state != "" {
		switch state {
		case integrationbus.AlertStateFiring, integrationbus.AlertStateAcknowledged, integrationbus.AlertStateResolved:
			filter.State = &state
		default:
			return errs.New(errs.InvalidArgument, errors.New("state must be firing, acknowledged, or resolved"))
		}
	}

	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'since': want RFC3339"))
		}
		filter.Since = &t
	}

	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		filter.Limit = n
	}

	events, err := a.integrationBus.QueryAlertHistory(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryalerthistory: orgID[%s]: %s", orgID, err)
	}

	out := make([]AlertEvent, len(events))
	for i, e := range events {
		out[i] = toAppAlertEvent(e)
	}

	return AlertEvents{Alerts: out}
}

// acknowledgeAlert takes ownership of an open alert, stopping re-notification
// without closing it.
// POST /v1/orgs/{org_id}/alerts/{alert_id}/acknowledge
func (a *app) acknowledgeAlert(ctx context.Context, r *http.Request) web.Encoder {
	event, errResp := a.loadAlert(ctx, r)
	if errResp != nil {
		return errResp
	}

	userID, err := mid.GetUserID(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	updated, err := a.integrationBus.AcknowledgeAlert(ctx, event.ID, userID)
	if err != nil {
		return errs.Errorf(errs.Internal, "acknowledgealert: %s", err)
	}

	return toAppAlertEvent(updated)
}

// resolveAlert closes an alert, freeing its dedup key so a later occurrence is
// reported as new.
// POST /v1/orgs/{org_id}/alerts/{alert_id}/resolve
func (a *app) resolveAlert(ctx context.Context, r *http.Request) web.Encoder {
	event, errResp := a.loadAlert(ctx, r)
	if errResp != nil {
		return errResp
	}

	updated, err := a.integrationBus.ResolveAlert(ctx, event.ID)
	if err != nil {
		return errs.Errorf(errs.Internal, "resolvealert: %s", err)
	}

	return toAppAlertEvent(updated)
}

// loadAlert fetches an alert and confirms it belongs to the path org, so an id
// from another tenant reads as absent rather than acting on it.
func (a *app) loadAlert(ctx context.Context, r *http.Request) (integrationbus.AlertEvent, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return integrationbus.AlertEvent{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	id, err := uuid.Parse(web.Param(r, "alert_id"))
	if err != nil {
		return integrationbus.AlertEvent{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	notFound := errs.New(errs.NotFound, errors.New("alert not found"))

	event, err := a.integrationBus.QueryAlertEvent(ctx, id)
	if err != nil {
		if errors.Is(err, integrationbus.ErrEventNotFound) {
			return integrationbus.AlertEvent{}, notFound
		}
		return integrationbus.AlertEvent{}, errs.Errorf(errs.Internal, "queryalertevent: %s", err)
	}

	if event.OrgID != orgID {
		return integrationbus.AlertEvent{}, notFound
	}

	return event, nil
}

// =============================================================================
// Maintenance windows

// MaintenanceWindow suppresses delivery for a period.
type MaintenanceWindow struct {
	ID          string  `json:"id"`
	ProjectID   *string `json:"projectId"`
	Reason      string  `json:"reason"`
	StartsAt    string  `json:"startsAt"`
	EndsAt      string  `json:"endsAt"`
	Active      bool    `json:"active"`
	DateCreated string  `json:"dateCreated"`
}

// MaintenanceWindows is the list response.
type MaintenanceWindows struct {
	Windows []MaintenanceWindow `json:"maintenanceWindows"`
}

// Encode implements the encoder interface.
func (app MaintenanceWindows) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Encode implements the encoder interface.
func (app MaintenanceWindow) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppWindow(w integrationbus.MaintenanceWindow, now time.Time) MaintenanceWindow {
	out := MaintenanceWindow{
		ID: w.ID.String(), Reason: w.Reason,
		StartsAt:    w.StartsAt.Format(time.RFC3339),
		EndsAt:      w.EndsAt.Format(time.RFC3339),
		Active:      w.Covers(uuid.Nil, now) || (w.ProjectID != nil && !now.Before(w.StartsAt) && now.Before(w.EndsAt)),
		DateCreated: w.DateCreated.Format(time.RFC3339),
	}
	if w.ProjectID != nil {
		s := w.ProjectID.String()
		out.ProjectID = &s
	}
	return out
}

// NewMaintenanceWindow is the create request body.
type NewMaintenanceWindow struct {
	ProjectID string `json:"projectId"`
	Reason    string `json:"reason"`
	StartsAt  string `json:"startsAt"`
	EndsAt    string `json:"endsAt"`
}

// Decode implements the decoder interface.
func (app *NewMaintenanceWindow) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// queryMaintenance lists an org's maintenance windows.
// GET /v1/orgs/{org_id}/maintenance-windows
func (a *app) queryMaintenance(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	windows, err := a.integrationBus.QueryMaintenanceWindows(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querymaintenance: orgID[%s]: %s", orgID, err)
	}

	now := time.Now()
	out := make([]MaintenanceWindow, len(windows))
	for i, w := range windows {
		out[i] = toAppWindow(w, now)
	}

	return MaintenanceWindows{Windows: out}
}

// createMaintenance schedules a suppression window.
// POST /v1/orgs/{org_id}/maintenance-windows
func (a *app) createMaintenance(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	var body NewMaintenanceWindow
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	startsAt, endsAt, errResp := parseWindowRange(body.StartsAt, body.EndsAt)
	if errResp != nil {
		return errResp
	}

	var projectID *uuid.UUID
	if body.ProjectID != "" {
		id, err := uuid.Parse(body.ProjectID)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
		}
		projectID = &id
	}

	w, err := a.integrationBus.CreateMaintenanceWindow(ctx, orgID, mid.GetSubjectID(ctx), projectID, body.Reason, startsAt, endsAt)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	return toAppWindow(w, time.Now())
}

// deleteMaintenance cancels a suppression window.
// DELETE /v1/orgs/{org_id}/maintenance-windows/{window_id}
func (a *app) deleteMaintenance(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	id, err := uuid.Parse(web.Param(r, "window_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	// Confirm it belongs to this org before deleting, so an id from another
	// tenant cannot be cancelled through this path.
	windows, err := a.integrationBus.QueryMaintenanceWindows(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querymaintenance: %s", err)
	}

	var found bool
	for _, w := range windows {
		if w.ID == id {
			found = true
			break
		}
	}
	if !found {
		return errs.New(errs.NotFound, errors.New("maintenance window not found"))
	}

	if err := a.integrationBus.DeleteMaintenanceWindow(ctx, id); err != nil {
		return errs.Errorf(errs.Internal, "deletemaintenance: %s", err)
	}

	return nil
}

func parseWindowRange(startsAt, endsAt string) (time.Time, time.Time, web.Encoder) {
	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return time.Time{}, time.Time{}, errs.New(errs.InvalidArgument, errors.New("invalid 'startsAt': want RFC3339"))
	}

	end, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		return time.Time{}, time.Time{}, errs.New(errs.InvalidArgument, errors.New("invalid 'endsAt': want RFC3339"))
	}

	if !end.After(start) {
		return time.Time{}, time.Time{}, errs.New(errs.InvalidArgument, errors.New("'endsAt' must be after 'startsAt'"))
	}

	return start, end, nil
}
