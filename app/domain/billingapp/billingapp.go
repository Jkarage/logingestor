// Package billingapp maintains the app layer for the billing domain.
package billingapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	stripe "github.com/stripe/stripe-go/v82"
	portalSession "github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	stripesubscription "github.com/stripe/stripe-go/v82/subscription"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	log           *logger.Logger
	orgBus        orgbus.ExtBusiness
	webhookSecret string
	appBaseURL    string
}

func newApp(cfg Config) *app {
	stripe.Key = cfg.StripeSecretKey
	return &app{
		log:           cfg.Log,
		orgBus:        cfg.OrgBus,
		webhookSecret: cfg.StripeWebhookSecret,
		appBaseURL:    cfg.AppBaseURL,
	}
}

// listPlans returns the full plan catalog. Public endpoint.
func (a *app) listPlans(ctx context.Context, _ *http.Request) web.Encoder {
	plans, err := a.orgBus.QueryAllPlans(ctx)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryallplans: %s", err)
	}
	return toAppPlans(plans)
}

// getBilling returns the org's current subscription status.
func (a *app) getBilling(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sub, err := a.orgBus.QuerySubscription(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return BillingStatus{
				PlanSlug:          orgbus.PlanFree.String(),
				PlanName:          "Free",
				Status:            orgbus.StatusActive.String(),
				CurrentPeriodEnd:  nil,
				CancelAtPeriodEnd: false,
			}
		}
		return errs.Errorf(errs.Internal, "querysubscription: %s", err)
	}

	return BillingStatus{
		PlanSlug:          sub.Plan.String(),
		PlanName:          sub.PlanName,
		Status:            sub.Status.String(),
		CurrentPeriodEnd:  periodEndString(sub.PeriodEnd),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
	}
}

// checkout creates a Stripe Checkout Session and returns the hosted URL.
func (a *app) checkout(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	var req CheckoutRequest
	if err := web.Decode(r, &req); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	if req.PlanSlug == "free" || req.PlanSlug == "enterprise" {
		return errs.Errorf(errs.InvalidArgument, "cannot checkout for plan %q", req.PlanSlug)
	}

	plan, err := a.orgBus.QueryPlanBySlug(ctx, req.PlanSlug)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "queryplanbyslug: %s", err)
	}

	if plan.StripePriceID == "" {
		return errs.Errorf(errs.InvalidArgument, "plan %q has no stripe price configured", req.PlanSlug)
	}

	sub, subErr := a.orgBus.QuerySubscription(ctx, orgID)
	if subErr != nil && !errors.Is(subErr, orgbus.ErrNotFound) {
		return errs.Errorf(errs.Internal, "querysubscription: %s", subErr)
	}

	// Block if already on a paid active subscription
	if subErr == nil && sub.Plan != orgbus.PlanFree && sub.Status == orgbus.StatusActive && !sub.CancelAtPeriodEnd {
		return errs.New(errs.Aborted, orgbus.ErrAlreadySubscribed)
	}

	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String("subscription"),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			Price:    stripe.String(plan.StripePriceID),
			Quantity: stripe.Int64(1),
		}},
		SuccessURL:        stripe.String(req.SuccessURL),
		CancelURL:         stripe.String(req.CancelURL),
		ClientReferenceID: stripe.String(orgID.String()),
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{
			Metadata: map[string]string{"org_id": orgID.String()},
		},
	}

	if subErr == nil && sub.StripeCustomerID != "" {
		params.Customer = stripe.String(sub.StripeCustomerID)
	}

	session, err := checkoutsession.New(params)
	if err != nil {
		return errs.Errorf(errs.Internal, "stripe checkout session: %s", err)
	}

	return URLResponse{URL: session.URL}
}

// portal creates a Stripe Customer Portal session.
func (a *app) portal(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sub, err := a.orgBus.QuerySubscription(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.InvalidArgument, orgbus.ErrNoBillingAccount)
		}
		return errs.Errorf(errs.Internal, "querysubscription: %s", err)
	}

	if sub.StripeCustomerID == "" {
		return errs.New(errs.InvalidArgument, orgbus.ErrNoBillingAccount)
	}

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(sub.StripeCustomerID),
		ReturnURL: stripe.String(a.appBaseURL + "/dashboard/billing"),
	}

	s, err := portalSession.New(params)
	if err != nil {
		return errs.Errorf(errs.Internal, "stripe portal session: %s", err)
	}

	return URLResponse{URL: s.URL}
}

// cancel schedules the Stripe subscription to cancel at period end.
func (a *app) cancel(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	sub, err := a.orgBus.QuerySubscription(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.InvalidArgument, orgbus.ErrNotSubscribed)
		}
		return errs.Errorf(errs.Internal, "querysubscription: %s", err)
	}

	if sub.Plan == orgbus.PlanFree {
		return errs.New(errs.InvalidArgument, orgbus.ErrNotSubscribed)
	}

	if sub.CancelAtPeriodEnd {
		return errs.New(errs.Aborted, orgbus.ErrAlreadyCancelled)
	}

	if sub.StripeSubscriptionID == "" {
		return errs.Errorf(errs.Internal, "cancel: subscription has no stripe ID")
	}

	if _, err := stripesubscription.Update(sub.StripeSubscriptionID, &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	}); err != nil {
		return errs.Errorf(errs.Internal, "stripe cancel: %s", err)
	}

	sub.CancelAtPeriodEnd = true
	sub.DateUpdated = time.Now()

	if err := a.orgBus.UpsertSubscription(ctx, sub); err != nil {
		return errs.Errorf(errs.Internal, "upsertsubscription: %s", err)
	}

	return CancelResponse{
		CancelAtPeriodEnd: true,
		CurrentPeriodEnd:  periodEndString(sub.PeriodEnd),
	}
}

// webhook handles Stripe webhook events.
func (a *app) webhook(ctx context.Context, r *http.Request) web.Encoder {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return errs.Errorf(errs.InvalidArgument, "webhook: read body: %s", err)
	}

	event, err := stripe.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), a.webhookSecret)
	if err != nil {
		return errs.Errorf(errs.InvalidArgument, "webhook: invalid signature: %s", err)
	}

	switch event.Type {
	case "checkout.session.completed":
		if err := a.handleCheckoutCompleted(ctx, event); err != nil {
			a.log.Error(ctx, "webhook", "event", event.Type, "err", err)
		}
	case "customer.subscription.updated":
		if err := a.handleSubscriptionUpdated(ctx, event); err != nil {
			a.log.Error(ctx, "webhook", "event", event.Type, "err", err)
		}
	case "customer.subscription.deleted":
		if err := a.handleSubscriptionDeleted(ctx, event); err != nil {
			a.log.Error(ctx, "webhook", "event", event.Type, "err", err)
		}
	case "invoice.payment_failed":
		if err := a.handleInvoicePaymentFailed(ctx, event); err != nil {
			a.log.Error(ctx, "webhook", "event", event.Type, "err", err)
		}
	case "invoice.paid":
		if err := a.handleInvoicePaid(ctx, event); err != nil {
			a.log.Error(ctx, "webhook", "event", event.Type, "err", err)
		}
	}

	return nil
}

// =============================================================================
// Webhook event handlers

func (a *app) handleCheckoutCompleted(ctx context.Context, event stripe.Event) error {
	var cs stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &cs); err != nil {
		return fmt.Errorf("unmarshal checkout session: %w", err)
	}

	orgIDStr := cs.Metadata["org_id"]
	if orgIDStr == "" {
		return fmt.Errorf("no org_id in checkout session metadata")
	}

	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		return fmt.Errorf("parse org_id: %w", err)
	}

	if cs.Subscription == nil || cs.Customer == nil {
		return fmt.Errorf("checkout session missing subscription or customer")
	}

	stripeSub, err := stripesubscription.Get(cs.Subscription.ID, nil)
	if err != nil {
		return fmt.Errorf("stripe get subscription: %w", err)
	}

	proPlan, err := a.orgBus.QueryPlanBySlug(ctx, "pro")
	if err != nil {
		return fmt.Errorf("query pro plan: %w", err)
	}

	periodStart, periodEnd := extractPeriodDates(stripeSub)
	now := time.Now()

	existing, err := a.orgBus.QuerySubscription(ctx, orgID)
	var sub orgbus.Subscription
	if err != nil && !errors.Is(err, orgbus.ErrNotFound) {
		return fmt.Errorf("query subscription: %w", err)
	}

	if errors.Is(err, orgbus.ErrNotFound) {
		sub = orgbus.Subscription{
			SubscriptionID: uuid.New(),
			OrgID:          orgID,
			DateCreated:    now,
		}
	} else {
		sub = existing
	}

	sub.PlanID = proPlan.PlanID
	sub.Plan = proPlan.Slug
	sub.PlanName = proPlan.Name
	sub.PlanFeatures = proPlan.Features
	sub.Status = orgbus.StatusActive
	sub.StripeCustomerID = cs.Customer.ID
	sub.StripeSubscriptionID = cs.Subscription.ID
	sub.PeriodStart = periodStart
	sub.PeriodEnd = periodEnd
	sub.CancelAtPeriodEnd = false
	sub.DateUpdated = now

	return a.orgBus.UpsertSubscription(ctx, sub)
}

func (a *app) handleSubscriptionUpdated(ctx context.Context, event stripe.Event) error {
	var s stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}

	periodStart, periodEnd := extractPeriodDates(&s)

	sub := orgbus.Subscription{
		Status:            normalizeStripeStatus(s.Status),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
		DateUpdated:       time.Now(),
	}

	return a.orgBus.UpdateSubscriptionByStripeID(ctx, s.ID, sub)
}

func (a *app) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	var s stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}

	freePlan, err := a.orgBus.QueryPlanBySlug(ctx, "free")
	if err != nil {
		return fmt.Errorf("query free plan: %w", err)
	}

	now := time.Now()
	sub := orgbus.Subscription{
		PlanID:               freePlan.PlanID,
		Plan:                 orgbus.PlanFree,
		PlanName:             freePlan.Name,
		PlanFeatures:         freePlan.Features,
		Status:               orgbus.StatusCancelled,
		StripeSubscriptionID: "",
		CancelAtPeriodEnd:    false,
		CancelledAt:          &now,
		DateUpdated:          now,
	}

	return a.orgBus.UpdateSubscriptionByStripeID(ctx, s.ID, sub)
}

func (a *app) handleInvoicePaymentFailed(ctx context.Context, event stripe.Event) error {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return fmt.Errorf("unmarshal invoice: %w", err)
	}

	subID := invoiceSubscriptionID(&inv)
	if subID == "" {
		return nil
	}

	sub := orgbus.Subscription{
		Status:      orgbus.StatusPastDue,
		DateUpdated: time.Now(),
	}

	return a.orgBus.UpdateSubscriptionByStripeID(ctx, subID, sub)
}

func (a *app) handleInvoicePaid(ctx context.Context, event stripe.Event) error {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return fmt.Errorf("unmarshal invoice: %w", err)
	}

	subID := invoiceSubscriptionID(&inv)
	if subID == "" {
		return nil
	}

	stripeSub, err := stripesubscription.Get(subID, nil)
	if err != nil {
		return fmt.Errorf("stripe get subscription: %w", err)
	}

	periodStart, periodEnd := extractPeriodDates(stripeSub)

	sub := orgbus.Subscription{
		Status:      orgbus.StatusActive,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		DateUpdated: time.Now(),
	}

	return a.orgBus.UpdateSubscriptionByStripeID(ctx, subID, sub)
}

// =============================================================================
// Helpers

// invoiceSubscriptionID extracts the subscription ID from an invoice.
// In Stripe API v2, the subscription is nested under Parent.SubscriptionDetails.
func invoiceSubscriptionID(inv *stripe.Invoice) string {
	if inv.Parent == nil {
		return ""
	}
	if inv.Parent.SubscriptionDetails == nil {
		return ""
	}
	if inv.Parent.SubscriptionDetails.Subscription == nil {
		return ""
	}
	return inv.Parent.SubscriptionDetails.Subscription.ID
}

func extractPeriodDates(s *stripe.Subscription) (start, end *time.Time) {
	if s == nil || len(s.Items.Data) == 0 {
		return nil, nil
	}
	item := s.Items.Data[0]
	ts := time.Unix(item.CurrentPeriodStart, 0)
	te := time.Unix(item.CurrentPeriodEnd, 0)
	return &ts, &te
}

func normalizeStripeStatus(s stripe.SubscriptionStatus) orgbus.SubscriptionStatus {
	switch s {
	case stripe.SubscriptionStatusActive:
		return orgbus.StatusActive
	case stripe.SubscriptionStatusTrialing:
		return orgbus.StatusTrialing
	case stripe.SubscriptionStatusPastDue:
		return orgbus.StatusPastDue
	case stripe.SubscriptionStatusCanceled:
		return orgbus.StatusCancelled
	default:
		return orgbus.StatusActive
	}
}
