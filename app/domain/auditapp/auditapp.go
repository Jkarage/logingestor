// Package auditapp maintains the app layer api for the audit domain.
package auditapp

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	auditBus auditbus.ExtBusiness
}

func newApp(auditBus auditbus.ExtBusiness) *app {
	return &app{
		auditBus: auditBus,
	}
}

// query returns all audit records (platform-wide, super_admin only).
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	qp, err := parseQueryParams(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	pg, err := page.Parse(qp.Page, "10")
	if err != nil {
		return errs.NewFieldErrors("page", err)
	}

	filter, err := parseFilter(qp)
	if err != nil {
		return err.(*errs.Error)
	}

	orderBy, err := order.Parse(orderByFields, qp.OrderBy, auditbus.DefaultOrderBy)
	if err != nil {
		return errs.NewFieldErrors("order", err)
	}

	adts, err := a.auditBus.Query(ctx, filter, orderBy, pg)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	total, err := a.auditBus.Count(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "count: %s", err)
	}

	nextCursor := ""
	if pg.Number()*pg.RowsPerPage() < total {
		nextCursor = strconv.Itoa(pg.Number() + 1)
	}

	return AuditResult{
		Entries:    toAppAudits(adts),
		Total:      total,
		NextCursor: nextCursor,
	}
}

// queryByOrg returns audit records scoped to a specific org (org_admin + super_admin).
func (a *app) queryByOrg(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	qp, err := parseQueryParams(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	pg, err := page.Parse(qp.Page, "10")
	if err != nil {
		return errs.NewFieldErrors("page", err)
	}

	filter, err := parseFilter(qp)
	if err != nil {
		return err.(*errs.Error)
	}

	// Scope results to this org regardless of any query param.
	filter.OrgID = &orgID

	orderBy, err := order.Parse(orderByFields, qp.OrderBy, auditbus.DefaultOrderBy)
	if err != nil {
		return errs.NewFieldErrors("order", err)
	}

	adts, err := a.auditBus.Query(ctx, filter, orderBy, pg)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	total, err := a.auditBus.Count(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "count: %s", err)
	}

	nextCursor := ""
	if pg.Number()*pg.RowsPerPage() < total {
		nextCursor = strconv.Itoa(pg.Number() + 1)
	}

	return AuditResult{
		Entries:    toAppAudits(adts),
		Total:      total,
		NextCursor: nextCursor,
	}
}
