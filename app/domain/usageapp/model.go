// Package usageapp maintains the app layer api for organization ingest usage.
package usageapp

import (
	"encoding/json"
	"time"

	"github.com/jkarage/logingestor/business/domain/usagebus"
)

// ProjectUsage is one project's totals over the window.
type ProjectUsage struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Events      int64  `json:"events"`
	Bytes       int64  `json:"bytes"`
	Dropped     int64  `json:"dropped"`
}

// Totals is the organization-wide sum over the window.
type Totals struct {
	Events  int64 `json:"events"`
	Bytes   int64 `json:"bytes"`
	Dropped int64 `json:"dropped"`
}

// QuotaLimit is one enforced daily ingest limit and today's usage against it.
type QuotaLimit struct {
	DailyEvents int64 `json:"dailyEvents"`
	UsedToday   int64 `json:"usedToday"`
	Unlimited   bool  `json:"unlimited"`
	Exceeded    bool  `json:"exceeded"`
}

// Quota reports both enforced daily ingest quotas. Infra covers source-key
// ingestion; App covers the JWT ingest endpoint. They are separate limits, as
// the plans already separate infra and app retention.
type Quota struct {
	Infra QuotaLimit `json:"infra"`
	App   QuotaLimit `json:"app"`
}

func toQuotaLimit(q usagebus.QuotaStatus) QuotaLimit {
	return QuotaLimit{
		DailyEvents: q.Quota,
		UsedToday:   q.Used,
		Unlimited:   q.Quota < 0,
		Exceeded:    q.Exceeded(),
	}
}

// UsageResponse is returned by GET /v1/orgs/{org_id}/usage.
type UsageResponse struct {
	From      string         `json:"from"`
	To        string         `json:"to"`
	ByProject []ProjectUsage `json:"byProject"`
	Total     Totals         `json:"total"`
	Quota     Quota          `json:"quota"`

	// PeriodEnd is the current billing period's end, or null when the org has no
	// subscription period recorded.
	PeriodEnd *string `json:"periodEnd"`
}

// Encode implements the encoder interface.
func (app UsageResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppUsage(bus usagebus.OrgUsage, infraQuota, appQuota usagebus.QuotaStatus, periodEnd *time.Time) UsageResponse {
	byProject := make([]ProjectUsage, len(bus.ByProject))
	for i, p := range bus.ByProject {
		byProject[i] = ProjectUsage{
			ProjectID:   p.ProjectID.String(),
			ProjectName: p.ProjectName,
			Events:      p.EventCount,
			Bytes:       p.ByteCount,
			Dropped:     p.DroppedCount,
		}
	}

	resp := UsageResponse{
		From:      bus.From.Format(time.RFC3339),
		To:        bus.To.Format(time.RFC3339),
		ByProject: byProject,
		Total: Totals{
			Events:  bus.EventCount,
			Bytes:   bus.ByteCount,
			Dropped: bus.DroppedCount,
		},
		Quota: Quota{
			Infra: toQuotaLimit(infraQuota),
			App:   toQuotaLimit(appQuota),
		},
	}

	if periodEnd != nil {
		v := periodEnd.UTC().Format(time.RFC3339)
		resp.PeriodEnd = &v
	}

	return resp
}
