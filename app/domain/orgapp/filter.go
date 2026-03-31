package orgapp

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/types/name"
)

type queryParams struct {
	Page    string
	Rows    string
	OrderBy string
	ID      string
	Name    string
	Slug    string
	Enabled string
}

func parseQueryParams(r *http.Request) (queryParams, error) {
	values := r.URL.Query()

	return queryParams{
		Page:    values.Get("page"),
		Rows:    values.Get("rows"),
		OrderBy: values.Get("orderBy"),
		ID:      values.Get("id"),
		Name:    values.Get("name"),
		Slug:    values.Get("slug"),
		Enabled: values.Get("enabled"),
	}, nil
}

func parseFilter(qp queryParams) (orgbus.QueryFilter, error) {
	var fieldErrors errs.FieldErrors
	var filter orgbus.QueryFilter

	if qp.ID != "" {
		id, err := uuid.Parse(qp.ID)
		if err != nil {
			fieldErrors.Add("id", err)
		} else {
			filter.ID = &id
		}
	}

	if qp.Name != "" {
		nme, err := name.Parse(qp.Name)
		if err != nil {
			fieldErrors.Add("name", err)
		} else {
			filter.Name = &nme
		}
	}

	if qp.Slug != "" {
		filter.Slug = &qp.Slug
	}

	if qp.Enabled != "" {
		enabled := qp.Enabled == "true"
		filter.Enabled = &enabled
	}

	if fieldErrors != nil {
		return orgbus.QueryFilter{}, fieldErrors.ToError()
	}

	return filter, nil
}
