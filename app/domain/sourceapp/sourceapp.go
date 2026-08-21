// Package sourceapp maintains the app layer api for the source domain.
package sourceapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	sourceBus  sourcebus.ExtBusiness
	projectBus projectbus.ExtBusiness

	// usageBus supplies the ingest counters health is derived from. It is
	// optional: with no usage store wired, sources still list and every one
	// reports the counters it has, which is none.
	usageBus usagebus.ExtBusiness

	// rejectBus reads the refused records. Optional for the same reason.
	rejectBus *rejectbus.Business
}

func newApp(sourceBus sourcebus.ExtBusiness, projectBus projectbus.ExtBusiness, usageBus usagebus.ExtBusiness, rejectBus *rejectbus.Business) *app {
	return &app{
		sourceBus:  sourceBus,
		projectBus: projectBus,
		usageBus:   usageBus,
		rejectBus:  rejectBus,
	}
}

// create handles POST /v1/orgs/{org_id}/sources.
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var ns NewSource
	if err := web.Decode(r, &ns); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if ns.Name == "" {
		return errs.New(errs.InvalidArgument, errors.New("name is required"))
	}

	// Bad kind: the repo's error set has no 422; surface as 400.
	if !sourcebus.ValidKind(ns.Kind) {
		return errs.Errorf(errs.InvalidArgument, "invalid kind %q", ns.Kind)
	}

	projectID, err := uuid.Parse(ns.ProjectID)
	if err != nil {
		return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
	}

	// The target project must exist and belong to this org (404 otherwise) so a
	// source can never be pointed at another tenant's project.
	project, err := a.projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return errs.New(errs.NotFound, errors.New("project not found in org"))
		}
		return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}
	if project.OrgID != orgID {
		return errs.New(errs.NotFound, errors.New("project not found in org"))
	}

	if ns.ExpiresInDays < 0 {
		return errs.New(errs.InvalidArgument, errors.New("expiresInDays must be zero (never expires) or positive"))
	}

	var expiresAt *time.Time
	if ns.ExpiresInDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, ns.ExpiresInDays)
		expiresAt = &t
	}

	source, rawKey, err := a.sourceBus.Create(ctx, mid.GetSubjectID(ctx), sourcebus.NewSource{
		OrgID:     orgID,
		ProjectID: projectID,
		Kind:      ns.Kind,
		Name:      ns.Name,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		if errors.Is(err, sourcebus.ErrDuplicateName) {
			return errs.New(errs.Aborted, sourcebus.ErrDuplicateName)
		}
		if errors.Is(err, sourcebus.ErrInvalidKind) {
			return errs.Errorf(errs.InvalidArgument, "invalid kind %q", ns.Kind)
		}
		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	created := SourceCreated{
		ID:        source.ID.String(),
		Kind:      source.Kind,
		Name:      source.Name,
		ProjectID: source.ProjectID.String(),
		IsActive:  source.IsActive,
		CreatedAt: source.CreatedAt.Format(time.RFC3339),
		IngestKey: rawKey,
		KeyPrefix: source.KeyPrefix,
	}

	if source.ExpiresAt != nil {
		v := source.ExpiresAt.Format(time.RFC3339)
		created.ExpiresAt = &v
	}

	return created
}

// query handles GET /v1/orgs/{org_id}/sources.
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sources, err := a.sourceBus.QueryByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	now := time.Now().UTC()

	// One counters query for the whole page rather than one per source: the
	// Sources UI shows health on every row, and a request per row is what makes a
	// list page slow as an org adds collectors.
	counters, err := a.healthCounters(ctx, sources, now)
	if err != nil {
		return errs.Errorf(errs.Internal, "healthcounters: orgID[%s]: %s", orgID, err)
	}

	return toAppSources(sources, counters, now)
}

// health handles GET /v1/orgs/{org_id}/sources/{source_id}/health.
func (a *app) health(ctx context.Context, r *http.Request) web.Encoder {
	source, errResp := a.loadOrgSource(ctx, r)
	if errResp != nil {
		return errResp
	}

	now := time.Now().UTC()
	end := now.Truncate(time.Hour).Add(time.Hour)
	start := end.Add(-sourcebus.HealthWindow)

	counters, err := a.healthCounters(ctx, []sourcebus.Source{source}, now)
	if err != nil {
		return errs.Errorf(errs.Internal, "healthcounters: sourceID[%s]: %s", source.ID, err)
	}

	h := source.Health(now, counters[source.ID])

	out := SourceHealth{
		SourceID:     source.ID.String(),
		Status:       string(h.Status),
		IsActive:     source.IsActive,
		Expired:      source.Expired(now),
		WindowStart:  start.Format(time.RFC3339),
		WindowEnd:    end.Format(time.RFC3339),
		Events24h:    h.Events,
		Errors24h:    h.Errors,
		Dropped24h:   h.Dropped,
		ErrorRate24h: h.ErrorRate,
		Buckets:      []HealthBucket{},
	}

	if source.ExpiresAt != nil {
		v := source.ExpiresAt.Format(time.RFC3339)
		out.ExpiresAt = &v
	}
	if source.LastSeenAt != nil {
		v := source.LastSeenAt.Format(time.RFC3339)
		out.LastSeenAt = &v
	}

	if a.usageBus != nil {
		buckets, err := a.usageBus.QuerySourceBuckets(ctx, source.ID, start, end)
		if err != nil {
			return errs.Errorf(errs.Internal, "querysourcebuckets: sourceID[%s]: %s", source.ID, err)
		}
		out.Buckets = fillBuckets(buckets, start, end)
	} else {
		out.Buckets = fillBuckets(nil, start, end)
	}

	return out
}

// healthCounters loads the ingest counters for a set of sources over the health
// window, keyed by source ID.
func (a *app) healthCounters(ctx context.Context, sources []sourcebus.Source, now time.Time) (map[uuid.UUID]sourcebus.HealthCounters, error) {
	out := make(map[uuid.UUID]sourcebus.HealthCounters, len(sources))
	if a.usageBus == nil || len(sources) == 0 {
		return out, nil
	}

	ids := make([]uuid.UUID, len(sources))
	for i, s := range sources {
		ids[i] = s.ID
	}

	from := now.Truncate(time.Hour).Add(time.Hour).Add(-sourcebus.HealthWindow)

	counters, err := a.usageBus.QuerySourceCounters(ctx, ids, from)
	if err != nil {
		return nil, err
	}

	for id, c := range counters {
		out[id] = sourcebus.HealthCounters{Events: c.Events, Errors: c.Errors, Dropped: c.Dropped}
	}

	return out, nil
}

// fillBuckets projects the hours that carry counters onto the full hourly grid of
// the window, so a client plots a fixed-width series and a gap reads as a gap
// rather than a missing point.
func fillBuckets(counters []usagebus.HourCounters, start, end time.Time) []HealthBucket {
	byHour := make(map[time.Time]usagebus.HourCounters, len(counters))
	for _, c := range counters {
		byHour[c.Hour.UTC()] = c
	}

	out := make([]HealthBucket, 0, int(end.Sub(start)/time.Hour))
	for h := start; h.Before(end); h = h.Add(time.Hour) {
		c := byHour[h]
		out = append(out, HealthBucket{
			Hour:    h.Format(time.RFC3339),
			Events:  c.Events,
			Errors:  c.Errors,
			Dropped: c.Dropped,
		})
	}

	return out
}

// disconnect handles DELETE /v1/orgs/{org_id}/sources/{source_id} — soft-disable.
func (a *app) disconnect(ctx context.Context, r *http.Request) web.Encoder {
	source, errResp := a.loadOrgSource(ctx, r)
	if errResp != nil {
		return errResp
	}

	if _, err := a.sourceBus.Disable(ctx, mid.GetSubjectID(ctx), source); err != nil {
		return errs.Errorf(errs.Internal, "disable: sourceID[%s]: %s", source.ID, err)
	}

	return Disconnected{Disconnected: true}
}

// rotateKey handles POST /v1/orgs/{org_id}/sources/{source_id}/rotate-key.
func (a *app) rotateKey(ctx context.Context, r *http.Request) web.Encoder {
	source, errResp := a.loadOrgSource(ctx, r)
	if errResp != nil {
		return errResp
	}

	updated, rawKey, err := a.sourceBus.RotateKey(ctx, mid.GetSubjectID(ctx), source)
	if err != nil {
		return errs.Errorf(errs.Internal, "rotatekey: sourceID[%s]: %s", source.ID, err)
	}

	return RotatedKey{IngestKey: rawKey, KeyPrefix: updated.KeyPrefix}
}

// loadOrgSource parses the org_id + source_id path params and loads the source,
// confirming it belongs to the org (404 otherwise). Returns a non-nil
// web.Encoder error response on failure.
func (a *app) loadOrgSource(ctx context.Context, r *http.Request) (sourcebus.Source, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return sourcebus.Source{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sourceID, err := uuid.Parse(web.Param(r, "source_id"))
	if err != nil {
		return sourcebus.Source{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	source, err := a.sourceBus.QueryByID(ctx, sourceID)
	if err != nil {
		if errors.Is(err, sourcebus.ErrNotFound) {
			return sourcebus.Source{}, errs.New(errs.NotFound, sourcebus.ErrNotFound)
		}
		return sourcebus.Source{}, errs.Errorf(errs.Internal, "querybyid: sourceID[%s]: %s", sourceID, err)
	}

	if source.OrgID != orgID {
		return sourcebus.Source{}, errs.New(errs.NotFound, fmt.Errorf("%w in org", sourcebus.ErrNotFound))
	}

	return source, nil
}
