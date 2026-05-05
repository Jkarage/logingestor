package billingapp

import (
	"encoding/json"
	"time"

	"github.com/jkarage/logingestor/business/domain/orgbus"
)

// Plan is the API response for a billing plan.
type Plan struct {
	ID         string         `json:"id"`
	Slug       string         `json:"slug"`
	Name       string         `json:"name"`
	PriceCents int            `json:"priceCents"`
	Interval   string         `json:"interval"`
	Features   map[string]int `json:"features"`
}

// Encode implements the web.Encoder interface.
func (p Plan) Encode() ([]byte, string, error) {
	data, err := json.Marshal(p)
	return data, "application/json", err
}

// PlansResponse wraps the plan list for JSON output.
type PlansResponse struct {
	Plans []Plan `json:"plans"`
}

// Encode implements the web.Encoder interface.
func (p PlansResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(p)
	return data, "application/json", err
}

// BillingStatus is the response for GET /orgs/:id/billing.
type BillingStatus struct {
	PlanSlug          string  `json:"planSlug"`
	PlanName          string  `json:"planName"`
	Status            string  `json:"status"`
	CurrentPeriodEnd  *string `json:"currentPeriodEnd"`
	CancelAtPeriodEnd bool    `json:"cancelAtPeriodEnd"`
}

// Encode implements the web.Encoder interface.
func (b BillingStatus) Encode() ([]byte, string, error) {
	data, err := json.Marshal(b)
	return data, "application/json", err
}

// CheckoutRequest is the body for POST /orgs/:id/billing/checkout.
type CheckoutRequest struct {
	PlanSlug   string `json:"planSlug"`
	SuccessURL string `json:"successUrl"`
	CancelURL  string `json:"cancelUrl"`
}

// Decode implements the web.Decoder interface.
func (c *CheckoutRequest) Decode(data []byte) error {
	return json.Unmarshal(data, c)
}

// URLResponse carries a redirect URL.
type URLResponse struct {
	URL string `json:"url"`
}

// Encode implements the web.Encoder interface.
func (u URLResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(u)
	return data, "application/json", err
}

// CancelResponse is the response for POST /orgs/:id/billing/cancel.
type CancelResponse struct {
	CancelAtPeriodEnd bool    `json:"cancelAtPeriodEnd"`
	CurrentPeriodEnd  *string `json:"currentPeriodEnd"`
}

// Encode implements the web.Encoder interface.
func (c CancelResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(c)
	return data, "application/json", err
}

// =============================================================================
// Converters

func toAppPlan(bus orgbus.Plan) Plan {
	return Plan{
		ID:         bus.PlanID.String(),
		Slug:       bus.Slug.String(),
		Name:       bus.Name,
		PriceCents: bus.PriceCents,
		Interval:   bus.Interval,
		Features: map[string]int{
			"log_retention_days": bus.Features.LogRetentionDays,
			"max_projects":       bus.Features.MaxProjects,
			"max_members":        bus.Features.MaxMembers,
		},
	}
}

func toAppPlans(busPlans []orgbus.Plan) PlansResponse {
	plans := make([]Plan, len(busPlans))
	for i, p := range busPlans {
		plans[i] = toAppPlan(p)
	}
	return PlansResponse{Plans: plans}
}

func periodEndString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}
