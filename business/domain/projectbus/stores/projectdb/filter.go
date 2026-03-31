package projectdb

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jkarage/logingestor/business/domain/projectbus"
)

func applyFilter(filter projectbus.QueryFilter, data map[string]any, buf *bytes.Buffer) {
	var wc []string

	if filter.ID != nil {
		data["id"] = filter.ID
		wc = append(wc, "id = :id")
	}

	if filter.OrgID != nil {
		data["org_id"] = filter.OrgID
		wc = append(wc, "org_id = :org_id")
	}

	if filter.Name != nil {
		data["name"] = fmt.Sprintf("%%%s%%", *filter.Name)
		wc = append(wc, "name LIKE :name")
	}

	if len(wc) > 0 {
		buf.WriteString(" WHERE ")
		buf.WriteString(strings.Join(wc, " AND "))
	}
}
