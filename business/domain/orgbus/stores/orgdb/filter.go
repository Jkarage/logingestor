package orgdb

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jkarage/logingestor/business/domain/orgbus"
)

func applyFilter(filter orgbus.QueryFilter, data map[string]any, buf *bytes.Buffer) {
	var wc []string

	if filter.ID != nil {
		data["id"] = filter.ID
		wc = append(wc, "id = :id")
	}

	if filter.Name != nil {
		data["name"] = fmt.Sprintf("%%%s%%", filter.Name)
		wc = append(wc, "name LIKE :name")
	}

	if filter.Slug != nil {
		data["slug"] = *filter.Slug
		wc = append(wc, "slug = :slug")
	}

	if filter.Enabled != nil {
		data["enabled"] = *filter.Enabled
		wc = append(wc, "enabled = :enabled")
	}

	if len(wc) > 0 {
		buf.WriteString(" WHERE ")
		buf.WriteString(strings.Join(wc, " AND "))
	}
}
