package auditdb

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jkarage/logingestor/business/domain/auditbus"
)

func applyFilter(filter auditbus.QueryFilter, data map[string]any, buf *bytes.Buffer) {
	var wc []string

	if filter.OrgID != nil {
		data["org_id"] = filter.OrgID
		wc = append(wc, "a.org_id = :org_id")
	}

	if filter.ObjID != nil {
		data["obj_id"] = filter.ObjID
		wc = append(wc, "a.obj_id = :obj_id")
	}

	if filter.ObjDomain != nil {
		data["obj_domain"] = filter.ObjDomain.String()
		wc = append(wc, "a.obj_domain = :obj_domain")
	}

	if filter.ObjName != nil {
		data["obj_name"] = fmt.Sprintf("%%%s%%", filter.ObjName.String())
		wc = append(wc, "a.obj_name LIKE :obj_name")
	}

	if filter.ActorID != nil {
		data["actor_id"] = filter.ActorID
		wc = append(wc, "a.actor_id = :actor_id")
	}

	if filter.Action != nil {
		data["action"] = filter.Action
		wc = append(wc, "a.action = :action")
	}

	if filter.Since != nil {
		data["since"] = filter.Since
		wc = append(wc, "a.timestamp >= :since")
	}

	if filter.Until != nil {
		data["until"] = filter.Until
		wc = append(wc, "a.timestamp <= :until")
	}

	if len(wc) > 0 {
		buf.WriteString(" WHERE ")
		buf.WriteString(strings.Join(wc, " AND "))
	}
}
