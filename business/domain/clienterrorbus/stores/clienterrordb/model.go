// Package clienterrordb contains client error monitoring database access.
package clienterrordb

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jmoiron/sqlx/types"
)

type eventDB struct {
	ID             uuid.UUID      `db:"id"`
	EventID        uuid.UUID      `db:"event_id"`
	OrgID          *uuid.UUID     `db:"org_id"`
	UserID         *uuid.UUID     `db:"user_id"`
	Role           *string        `db:"role"`
	Level          string         `db:"level"`
	Kind           string         `db:"kind"`
	Name           string         `db:"name"`
	Message        string         `db:"message"`
	Stack          string         `db:"stack"`
	ComponentStack string         `db:"component_stack"`
	Release        string         `db:"release"`
	Environment    string         `db:"environment"`
	URL            string         `db:"url"`
	UserAgent      string         `db:"user_agent"`
	API            types.JSONText `db:"api"`
	Breadcrumbs    types.JSONText `db:"breadcrumbs"`
	OccurredAt     time.Time      `db:"occurred_at"`
	ReceivedAt     time.Time      `db:"received_at"`
	Fingerprint    *string        `db:"fingerprint"`
	IssueID        *uuid.UUID     `db:"issue_id"`
	SampledCount   int            `db:"sampled_count"`
}

// breadcrumbDB is the stored shape of a breadcrumb. The JSON keys are short
// because a batch can carry thirty of them per event.
type breadcrumbDB struct {
	TS       time.Time `json:"ts"`
	Category string    `json:"category,omitempty"`
	Message  string    `json:"message,omitempty"`
}

func toDBEvent(e clienterrorbus.Event) eventDB {
	crumbs := make([]breadcrumbDB, 0, len(e.Breadcrumbs))
	for _, c := range e.Breadcrumbs {
		crumbs = append(crumbs, breadcrumbDB{TS: c.TS.UTC(), Category: c.Category, Message: c.Message})
	}

	// Marshal errors are impossible for these shapes; an empty array keeps the
	// NOT NULL column valid either way.
	crumbJSON, err := json.Marshal(crumbs)
	if err != nil {
		crumbJSON = []byte("[]")
	}

	db := eventDB{
		ID: e.ID, EventID: e.EventID, OrgID: e.OrgID, UserID: e.UserID,
		Level: e.Level, Kind: e.Kind, Name: e.Name, Message: e.Message,
		Stack: e.Stack, ComponentStack: e.ComponentStack,
		Release: e.Release, Environment: e.Environment, URL: e.URL, UserAgent: e.UserAgent,
		Breadcrumbs:  types.JSONText(crumbJSON),
		OccurredAt:   e.OccurredAt.UTC(),
		ReceivedAt:   e.ReceivedAt.UTC(),
		SampledCount: e.SampledCount,
	}

	if e.Role != "" {
		role := e.Role
		db.Role = &role
	}

	if e.API != nil {
		if apiJSON, err := json.Marshal(e.API); err == nil {
			db.API = types.JSONText(apiJSON)
		}
	}

	return db
}

func toBusEvent(db eventDB) clienterrorbus.Event {
	e := clienterrorbus.Event{
		ID: db.ID, EventID: db.EventID, OrgID: db.OrgID, UserID: db.UserID,
		Level: db.Level, Kind: db.Kind, Name: db.Name, Message: db.Message,
		Stack: db.Stack, ComponentStack: db.ComponentStack,
		Release: db.Release, Environment: db.Environment, URL: db.URL, UserAgent: db.UserAgent,
		OccurredAt:   db.OccurredAt.UTC(),
		ReceivedAt:   db.ReceivedAt.UTC(),
		IssueID:      db.IssueID,
		SampledCount: db.SampledCount,
	}

	if db.Role != nil {
		e.Role = *db.Role
	}
	if db.Fingerprint != nil {
		e.Fingerprint = *db.Fingerprint
	}

	if len(db.API) > 0 {
		var api clienterrorbus.APIContext
		if err := json.Unmarshal(db.API, &api); err == nil {
			e.API = &api
		}
	}

	if len(db.Breadcrumbs) > 0 {
		var crumbs []breadcrumbDB
		if err := json.Unmarshal(db.Breadcrumbs, &crumbs); err == nil {
			for _, c := range crumbs {
				e.Breadcrumbs = append(e.Breadcrumbs, clienterrorbus.Breadcrumb{
					TS: c.TS.UTC(), Category: c.Category, Message: c.Message,
				})
			}
		}
	}

	return e
}

type issueDB struct {
	ID            uuid.UUID  `db:"id"`
	OrgID         *uuid.UUID `db:"org_id"`
	Fingerprint   string     `db:"fingerprint"`
	Title         string     `db:"title"`
	Culprit       string     `db:"culprit"`
	Level         string     `db:"level"`
	Kind          string     `db:"kind"`
	Status        string     `db:"status"`
	Regressed     bool       `db:"regressed"`
	EventCount    int64      `db:"event_count"`
	FirstSeenAt   time.Time  `db:"first_seen_at"`
	LastSeenAt    time.Time  `db:"last_seen_at"`
	ResolvedAt    *time.Time `db:"resolved_at"`
	AssigneeID    *uuid.UUID `db:"assignee_id"`
	SampleEventID *uuid.UUID `db:"sample_event_id"`

	// Filled by the listing query, which counts the facet rows.
	AffectedUsers int     `db:"affected_users"`
	AffectedOrgs  int     `db:"affected_orgs"`
	Releases      *string `db:"releases"`
}

func toBusIssue(db issueDB) clienterrorbus.Issue {
	i := clienterrorbus.Issue{
		ID: db.ID, OrgID: db.OrgID, Fingerprint: db.Fingerprint,
		Title: db.Title, Culprit: db.Culprit, Level: db.Level, Kind: db.Kind,
		Status: db.Status, Regressed: db.Regressed, EventCount: db.EventCount,
		FirstSeenAt: db.FirstSeenAt.UTC(), LastSeenAt: db.LastSeenAt.UTC(),
		ResolvedAt: db.ResolvedAt, AssigneeID: db.AssigneeID, SampleEventID: db.SampleEventID,
		AffectedUsers: db.AffectedUsers, AffectedOrgs: db.AffectedOrgs,
	}

	if db.Releases != nil && *db.Releases != "" {
		i.Releases = splitCSV(*db.Releases)
	}

	return i
}

// splitCSV unpacks the comma-joined release list the listing query aggregates.
// A release name cannot contain a comma: it is a build identifier, validated
// upstream and truncated on the way in, so joining is safe.
func splitCSV(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}
