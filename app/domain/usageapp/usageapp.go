package usageapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/foundation/web"
)

// maxWindow bounds the reported range. ingest_usage is a daily rollup with a few
// rows per source per day, so this is a sanity bound rather than a cost control.
const maxWindow = 400 * 24 * time.Hour

// defaultWindow is used when the caller supplies no range and the org has no
// billing period to fall back on.
const defaultWindow = 30 * 24 * time.Hour

type app struct {
	usageBus usagebus.ExtBusiness
	orgBus   orgbus.ExtBusiness
}

func newApp(usageBus usagebus.ExtBusiness, orgBus orgbus.ExtBusiness) *app {
	return &app{usageBus: usageBus, orgBus: orgBus}
}

// query reports ingest usage for an organization.
// GET /v1/orgs/{org_id}/usage?from&to
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	// The billing period is both the default window and part of the response, so
	// resolve it before parsing the range. A missing subscription is not fatal:
	// usage is still reportable.
	var periodStart, periodEnd *time.Time
	if sub, err := a.orgBus.QuerySubscription(ctx, orgID); err == nil {
		periodStart, periodEnd = sub.PeriodStart, sub.PeriodEnd
	} else if !errors.Is(err, orgbus.ErrNoBillingAccount) && !errors.Is(err, orgbus.ErrNotFound) {
		return errs.Errorf(errs.Internal, "querysubscription: orgID[%s]: %s", orgID, err)
	}

	from, to, errResp := parseWindow(r, periodStart)
	if errResp != nil {
		return errResp
	}

	usage, err := a.usageBus.QueryByOrg(ctx, orgID, from, to)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryusage: orgID[%s]: %s", orgID, err)
	}

	now := time.Now().UTC()

	infraQuota, err := a.usageBus.CheckQuota(ctx, orgID, now)
	if err != nil {
		return errs.Errorf(errs.Internal, "checkquota: orgID[%s]: %s", orgID, err)
	}

	appQuota, err := a.usageBus.CheckAppQuota(ctx, orgID, now)
	if err != nil {
		return errs.Errorf(errs.Internal, "checkappquota: orgID[%s]: %s", orgID, err)
	}

	return toAppUsage(usage, infraQuota, appQuota, periodEnd)
}

// parseWindow resolves the reporting range. Defaults, in order: the caller's
// explicit values, the current billing period start, then a 30-day lookback.
func parseWindow(r *http.Request, periodStart *time.Time) (from, to time.Time, resp web.Encoder) {
	q := r.URL.Query()

	// The rollup is keyed by day, so the window is exclusive of tomorrow.
	to = time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)

	switch {
	case periodStart != nil:
		from = periodStart.UTC()
	default:
		from = to.Add(-defaultWindow)
	}

	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"from", &from}, {"to", &to}} {
		s := q.Get(f.name)
		if s == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return from, to, errs.Errorf(errs.InvalidArgument, "invalid '%s': want RFC3339", f.name)
		}
		*f.dst = t.UTC()
	}

	if !from.Before(to) {
		return from, to, errs.New(errs.InvalidArgument, errors.New("'from' must be before 'to'"))
	}

	if to.Sub(from) > maxWindow {
		return from, to, errs.Errorf(errs.InvalidArgument, "range exceeds the %d day maximum", int(maxWindow.Hours()/24))
	}

	return from, to, nil
}
