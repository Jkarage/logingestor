// Package clientalert delivers client error issues through the existing alert
// rules and integration channels.
//
// It is an adapter and nothing else. Everything that makes alerting bearable —
// per-rule conditions, the dedup window, snooze, maintenance windows, the alert
// history, the provider callers — already exists and is project-scoped, so a
// client error with a project routes through all of it unchanged. Building a
// second delivery path would mean a second set of channels to configure and a
// second thing to silence during a deploy.
package clientalert

import (
	"context"
	"fmt"

	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Source is the value client error alerts carry in the payload's Source field.
//
// It is how a rule tells frontend crashes apart from log events: a condition
// naming this source matches only these, and a rule that names a different
// source no longer matches them at all.
const Source = "client-errors"

// Notifier implements clienterrorbus.Notifier over the alerting domain.
type Notifier struct {
	log    *logger.Logger
	alerts *integrationbus.Business
}

// New constructs the adapter.
func New(log *logger.Logger, alerts *integrationbus.Business) *Notifier {
	return &Notifier{log: log, alerts: alerts}
}

// IssueOpened delivers a first-sighting alert.
func (n *Notifier) IssueOpened(ctx context.Context, issue clienterrorbus.Issue, sample clienterrorbus.Event) {
	n.fire(ctx, issue, sample, "New client error")
}

// IssueRegressed delivers a regression alert — an issue somebody had closed
// that has started happening again.
func (n *Notifier) IssueRegressed(ctx context.Context, issue clienterrorbus.Issue, sample clienterrorbus.Event) {
	n.fire(ctx, issue, sample, "Client error regressed")
}

// IssueSpiked delivers a rate-change alert for a known issue — the case that is
// neither new nor a regression, and the one a bad deploy usually produces.
func (n *Notifier) IssueSpiked(ctx context.Context, issue clienterrorbus.Issue, spike clienterrorbus.Spike) {
	if n.alerts == nil || issue.ProjectID == nil {
		return
	}

	// The numbers are the alert: "spiking" without them tells nobody whether to
	// stop the rollout.
	detail := fmt.Sprintf("%s: %s — %d in the last %s, %.0f× the previous rate",
		"Client error spiking", issue.Title, spike.Current, spike.Window, spike.Multiple())

	payload := integrationbus.AlertPayload{
		ProjectName: "Client error spiking",
		Level:       alertLevel(issue.Level),
		Message:     detail,
		Source:      Source,
		Timestamp:   issue.LastSeenAt,
	}

	if issue.Culprit != "" {
		payload.Message += " (" + issue.Culprit + ")"
	}

	if err := n.alerts.FireAlerts(ctx, *issue.ProjectID, []integrationbus.AlertPayload{payload}); err != nil {
		n.log.Error(ctx, "clientalert: spike delivery failed", "issueID", issue.ID, "err", err)
	}
}

// fire hands one issue to the project's alert rules.
//
// An issue with no project cannot be delivered: rules and connections belong to
// projects, and there is nothing to fall back to that would not mean guessing
// which team to wake. Those issues still group and appear in the dashboard.
func (n *Notifier) fire(ctx context.Context, issue clienterrorbus.Issue, sample clienterrorbus.Event, prefix string) {
	if n.alerts == nil || issue.ProjectID == nil {
		return
	}

	payload := integrationbus.AlertPayload{
		ProjectName: prefix,
		Level:       alertLevel(issue.Level),
		Message:     fmt.Sprintf("%s: %s", prefix, issue.Title),
		Source:      Source,
		Timestamp:   issue.LastSeenAt,
	}

	if issue.Culprit != "" {
		payload.Message += " — " + issue.Culprit
	}
	if sample.URL != "" {
		payload.Message += " (" + sample.URL + ")"
	}

	// Delivery is best-effort and must never fail grouping: the issue is already
	// recorded, and a channel that is down is not a reason to lose it.
	if err := n.alerts.FireAlerts(ctx, *issue.ProjectID, []integrationbus.AlertPayload{payload}); err != nil {
		n.log.Error(ctx, "clientalert: delivery failed", "issueID", issue.ID, "err", err)
	}
}

// alertLevel maps a client error level onto the four log levels the alert rules
// are written against, so an existing rule means the same thing here.
//
// A fatal browser error is an ERROR rather than a level of its own: the rules
// have four levels and inventing a fifth would make every existing condition
// ambiguous.
func alertLevel(level string) string {
	switch level {
	case clienterrorbus.LevelWarning:
		return "WARN"
	default:
		return "ERROR"
	}
}
