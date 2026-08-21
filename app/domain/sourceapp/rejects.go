package sourceapp

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
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// Reject is a refused record as the API returns it.
type Reject struct {
	ID          string `json:"id"`
	SourceID    string `json:"sourceId"`
	ProjectID   string `json:"projectId"`
	Kind        string `json:"kind"`
	RecordIndex int    `json:"recordIndex"`
	Reason      string `json:"reason"`

	// Payload is the offending record, scrubbed and truncated.
	Payload    string `json:"payload"`
	ReceivedAt string `json:"receivedAt"`
}

// Rejects is the list response shape.
type Rejects struct {
	Rejects []Reject `json:"rejects"`

	// Counts totals what is stored per kind over the window.
	Counts map[string]int64 `json:"counts"`

	// HourlyCap is how many refusals are kept per source per hour. It is
	// reported because the list is a sample: past the cap records are counted
	// and not stored, and a reader who assumed otherwise would conclude the
	// problem had stopped.
	HourlyCap int `json:"hourlyCap"`
}

// Encode implements the web.Encoder interface.
func (app Rejects) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppReject(r rejectbus.Reject) Reject {
	return Reject{
		ID:          r.ID.String(),
		SourceID:    r.SourceID.String(),
		ProjectID:   r.ProjectID.String(),
		Kind:        r.Kind,
		RecordIndex: r.RecordIndex,
		Reason:      r.Reason,
		Payload:     r.Payload,
		ReceivedAt:  r.ReceivedAt.Format(time.RFC3339),
	}
}

// queryRejects handles GET /v1/orgs/{org_id}/ingest-rejects.
//
// This is the answer to "three hundred of my events are being refused and I do
// not know which". The count has always been in the response to the shipper;
// what was missing is the record itself, which is the only thing that explains
// the refusal once nobody is reading those responses.
func (a *app) queryRejects(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if a.rejectBus == nil {
		return errs.New(errs.Unimplemented, errors.New("rejected records are not being kept"))
	}

	q := r.URL.Query()
	filter := rejectbus.Filter{OrgID: orgID}

	for _, f := range []struct {
		name string
		dst  **uuid.UUID
	}{{"projectId", &filter.ProjectID}, {"sourceId", &filter.SourceID}} {
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

	if kind := q.Get("kind"); kind != "" {
		if kind != rejectbus.KindParse && kind != rejectbus.KindValidate {
			return errs.New(errs.InvalidArgument, errors.New("kind must be parse or validate"))
		}
		filter.Kind = kind
	}

	// The window bounds both the list and the counts, so the two agree.
	since := time.Now().UTC().Add(-24 * time.Hour)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'since': want RFC3339"))
		}
		since = t
	}
	filter.Since = &since

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return errs.New(errs.InvalidArgument, errors.New("invalid 'limit'"))
		}
		filter.Limit = n
	}

	rejects, err := a.rejectBus.Query(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryrejects: orgID[%s]: %s", orgID, err)
	}

	counts, err := a.rejectBus.CountByKind(ctx, orgID, since)
	if err != nil {
		return errs.Errorf(errs.Internal, "countbykind: orgID[%s]: %s", orgID, err)
	}

	out := Rejects{
		Rejects:   make([]Reject, len(rejects)),
		Counts:    counts,
		HourlyCap: a.rejectBus.HourlyCap(),
	}
	for i, rj := range rejects {
		out.Rejects[i] = toAppReject(rj)
	}

	return out
}
