package projectapp

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
)

type queryParams struct {
	Page    string
	Rows    string
	OrderBy string
	Name    string
}

func parseQueryParams(r *http.Request) (queryParams, error) {
	values := r.URL.Query()

	return queryParams{
		Page:    values.Get("page"),
		Rows:    values.Get("rows"),
		OrderBy: values.Get("orderBy"),
		Name:    values.Get("name"),
	}, nil
}

func parseFilter(qp queryParams) (projectbus.QueryFilter, error) {
	var fieldErrors errs.FieldErrors
	var filter projectbus.QueryFilter

	if qp.Name != "" {
		filter.Name = &qp.Name
	}

	if fieldErrors != nil {
		return projectbus.QueryFilter{}, fieldErrors.ToError()
	}

	_ = uuid.UUID{} // ensure uuid import is used via filter types

	return filter, nil
}
