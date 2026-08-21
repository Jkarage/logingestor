// Package clienterrorapp maintains the app layer api for client error
// monitoring: the public ingest endpoint and the triage API behind it.
package clienterrorapp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
)

// IngestRequest is the batch a browser posts.
type IngestRequest struct {
	Events []IngestEvent `json:"events"`
}

// Decode implements the decoder interface.
func (app *IngestRequest) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// IngestEvent is one reported error.
//
// UserID, OrgID and Role are accepted but treated as hints: the server derives
// them from the presented token and cross-checks the org against the caller's
// memberships, so a report cannot be filed against somebody else's tenant.
type IngestEvent struct {
	EventID        string `json:"eventId"`
	Timestamp      string `json:"timestamp"`
	Level          string `json:"level"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Message        string `json:"message"`
	Stack          string `json:"stack"`
	ComponentStack string `json:"componentStack"`

	Release     string `json:"release"`
	Environment string `json:"environment"`
	URL         string `json:"url"`
	UserAgent   string `json:"userAgent"`

	UserID string `json:"userId"`
	OrgID  string `json:"orgId"`

	// ProjectID is the project the user was working in when it broke. Like the
	// other identity fields it is a hint, honoured only if that project belongs
	// to the reporter's org — but unlike them it decides whether the issue can
	// alert, because rules and channels are project-scoped.
	ProjectID string `json:"projectId"`
	Role      string `json:"role"`

	API *struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Status int    `json:"status"`
		Code   string `json:"code"`
	} `json:"api"`

	Breadcrumbs []struct {
		TS       string `json:"ts"`
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"breadcrumbs"`

	// SampledCount lets a client that dropped repeats say how many this event
	// stands for, so totals stay honest under sampling.
	SampledCount int `json:"sampledCount"`
}

// toBusEvents maps the payload onto the domain, ignoring the client's identity
// hints entirely.
func toBusEvents(req IngestRequest) []clienterrorbus.NewEvent {
	out := make([]clienterrorbus.NewEvent, 0, len(req.Events))

	for _, e := range req.Events {
		ne := clienterrorbus.NewEvent{
			Level:          e.Level,
			Kind:           e.Kind,
			Name:           e.Name,
			Message:        e.Message,
			Stack:          e.Stack,
			ComponentStack: e.ComponentStack,
			Release:        e.Release,
			Environment:    e.Environment,
			URL:            e.URL,
			UserAgent:      e.UserAgent,
			SampledCount:   e.SampledCount,
		}

		// A malformed id is replaced rather than rejected: losing the only report
		// of a crash to a formatting mistake is the worse outcome.
		if id, err := uuid.Parse(e.EventID); err == nil {
			ne.EventID = id
		}
		if ts, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ne.OccurredAt = ts
		}

		if e.API != nil {
			ne.API = &clienterrorbus.APIContext{
				Method: e.API.Method, Path: e.API.Path,
				Status: e.API.Status, Code: e.API.Code,
			}
		}

		for _, c := range e.Breadcrumbs {
			crumb := clienterrorbus.Breadcrumb{Category: c.Category, Message: c.Message}
			if ts, err := time.Parse(time.RFC3339, c.TS); err == nil {
				crumb.TS = ts
			}
			ne.Breadcrumbs = append(ne.Breadcrumbs, crumb)
		}

		out = append(out, ne)
	}

	return out
}

// Accepted is the ingest response. It says what was stored, not what was
// processed: grouping happens after the browser has gone.
type Accepted struct {
	Accepted int `json:"accepted"`
}

// Encode implements the encoder interface.
func (app Accepted) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// HTTPStatus returns 202: the report is stored, the work is not done.
func (app Accepted) HTTPStatus() int { return http.StatusAccepted }

// Issue is a group of errors as the dashboard reads it.
type Issue struct {
	ID            string   `json:"id"`
	OrgID         *string  `json:"orgId"`
	ProjectID     *string  `json:"projectId"`
	Title         string   `json:"title"`
	Culprit       string   `json:"culprit"`
	Level         string   `json:"level"`
	Kind          string   `json:"kind"`
	Status        string   `json:"status"`
	Regressed     bool     `json:"regressed"`
	EventCount    int64    `json:"eventCount"`
	AffectedUsers int      `json:"affectedUsers"`
	AffectedOrgs  int      `json:"affectedOrgs"`
	Releases      []string `json:"releases"`
	FirstSeenAt   string   `json:"firstSeenAt"`
	LastSeenAt    string   `json:"lastSeenAt"`
	ResolvedAt    *string  `json:"resolvedAt"`
	AssigneeID    *string  `json:"assigneeId"`
}

// Encode implements the encoder interface.
func (app Issue) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Issues is the list response shape.
type Issues struct {
	Issues     []Issue `json:"issues"`
	NextCursor *string `json:"nextCursor"`
}

// Encode implements the encoder interface.
func (app Issues) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppIssue(i clienterrorbus.Issue) Issue {
	out := Issue{
		ID:            i.ID.String(),
		Title:         i.Title,
		Culprit:       i.Culprit,
		Level:         i.Level,
		Kind:          i.Kind,
		Status:        i.Status,
		Regressed:     i.Regressed,
		EventCount:    i.EventCount,
		AffectedUsers: i.AffectedUsers,
		AffectedOrgs:  i.AffectedOrgs,
		Releases:      i.Releases,
		FirstSeenAt:   i.FirstSeenAt.Format(time.RFC3339),
		LastSeenAt:    i.LastSeenAt.Format(time.RFC3339),
	}

	if out.Releases == nil {
		out.Releases = []string{}
	}
	if i.OrgID != nil {
		s := i.OrgID.String()
		out.OrgID = &s
	}
	if i.ProjectID != nil {
		s := i.ProjectID.String()
		out.ProjectID = &s
	}
	if i.ResolvedAt != nil {
		s := i.ResolvedAt.Format(time.RFC3339)
		out.ResolvedAt = &s
	}
	if i.AssigneeID != nil {
		s := i.AssigneeID.String()
		out.AssigneeID = &s
	}

	return out
}

// Event is one stored report as the detail view reads it.
type Event struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Stack   string `json:"stack"`

	// ResolvedStack is the de-minified stack, present once a source map for the
	// event's release has been uploaded. Render this when it is there and fall
	// back to Stack when it is not — a report can arrive before its map does.
	ResolvedStack  string         `json:"resolvedStack,omitempty"`
	ComponentStack string         `json:"componentStack,omitempty"`
	Release        string         `json:"release"`
	Environment    string         `json:"environment"`
	URL            string         `json:"url"`
	UserAgent      string         `json:"userAgent"`
	UserID         *string        `json:"userId"`
	OrgID          *string        `json:"orgId"`
	ProjectID      *string        `json:"projectId"`
	Role           string         `json:"role,omitempty"`
	API            map[string]any `json:"api,omitempty"`
	Breadcrumbs    []Breadcrumb   `json:"breadcrumbs"`
	OccurredAt     string         `json:"occurredAt"`
	ReceivedAt     string         `json:"receivedAt"`
	SampledCount   int            `json:"sampledCount"`
}

// Breadcrumb is one recent action before the error.
type Breadcrumb struct {
	TS       string `json:"ts"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

func toAppEvent(e clienterrorbus.Event) Event {
	out := Event{
		ID:             e.ID.String(),
		Level:          e.Level,
		Kind:           e.Kind,
		Name:           e.Name,
		Message:        e.Message,
		Stack:          e.Stack,
		ResolvedStack:  e.ResolvedStack,
		ComponentStack: e.ComponentStack,
		Release:        e.Release,
		Environment:    e.Environment,
		URL:            e.URL,
		UserAgent:      e.UserAgent,
		Role:           e.Role,
		Breadcrumbs:    []Breadcrumb{},
		OccurredAt:     e.OccurredAt.Format(time.RFC3339),
		ReceivedAt:     e.ReceivedAt.Format(time.RFC3339),
		SampledCount:   e.SampledCount,
	}

	if e.UserID != nil {
		s := e.UserID.String()
		out.UserID = &s
	}
	if e.OrgID != nil {
		s := e.OrgID.String()
		out.OrgID = &s
	}
	if e.ProjectID != nil {
		s := e.ProjectID.String()
		out.ProjectID = &s
	}
	if e.API != nil {
		out.API = map[string]any{
			"method": e.API.Method, "path": e.API.Path,
			"status": e.API.Status, "code": e.API.Code,
		}
	}
	for _, c := range e.Breadcrumbs {
		out.Breadcrumbs = append(out.Breadcrumbs, Breadcrumb{
			TS: c.TS.Format(time.RFC3339), Category: c.Category, Message: c.Message,
		})
	}

	return out
}

// IssueDetail is an issue with everything the detail view needs in one call.
type IssueDetail struct {
	Issue
	LatestEvent *Event  `json:"latestEvent"`
	Events      []Event `json:"events"`
	Series      []Point `json:"series"`
	Interval    string  `json:"interval"`
}

// Point is one bucket of the count-over-time series.
type Point struct {
	TS    string `json:"ts"`
	Count int64  `json:"count"`
}

// Encode implements the encoder interface.
func (app IssueDetail) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// UpdateIssueRequest is the triage body.
type UpdateIssueRequest struct {
	Status *string `json:"status"`

	// AssigneeID is a JSON pointer-ish tri-state: absent leaves the assignee,
	// null clears it, a uuid sets it.
	AssigneeID json.RawMessage `json:"assigneeId"`
}

// Decode implements the decoder interface.
func (app *UpdateIssueRequest) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// Stats are the dashboard tiles.
type Stats struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Events        int64  `json:"events"`
	Issues        int64  `json:"issues"`
	NewIssues     int64  `json:"newIssues"`
	Unresolved    int64  `json:"unresolved"`
	AffectedUsers int    `json:"affectedUsers"`
}

// Encode implements the encoder interface.
func (app Stats) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Purged reports a deletion.
type Purged struct {
	Purged int64 `json:"purged"`
}

// Encode implements the encoder interface.
func (app Purged) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Spike is a spiking issue as the dashboard reads it: the issue plus the two
// numbers that say how bad it has got.
type Spike struct {
	Issue

	// Current is how many events landed in the window.
	Current int64 `json:"current"`

	// Baseline is the rate before it, scaled to the same window length so the
	// two are directly comparable.
	Baseline float64 `json:"baseline"`

	// Multiple is Current over Baseline, which is the number worth showing.
	Multiple float64 `json:"multiple"`

	// Window is how long the current period is, as a duration string.
	Window string `json:"window"`
}

// Spikes is the list response shape.
type Spikes struct {
	Spikes []Spike `json:"spikes"`

	// Window, Baseline, Multiplier and MinEvents are the thresholds in force, so
	// a client can explain why something is or is not listed.
	Window     string  `json:"window"`
	Baseline   string  `json:"baseline"`
	Multiplier float64 `json:"multiplier"`
	MinEvents  int     `json:"minEvents"`
}

// Encode implements the encoder interface.
func (app Spikes) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppSpike(s clienterrorbus.Spike) Spike {
	return Spike{
		Issue:    toAppIssue(s.Issue),
		Current:  s.Current,
		Baseline: s.Baseline,
		Multiple: s.Multiple(),
		Window:   s.Window.String(),
	}
}
