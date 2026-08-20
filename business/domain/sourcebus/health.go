package sourcebus

import "time"

// HealthWindow is the period source health describes. It matches the events24h
// and errors24h figures the Sources UI shows.
const HealthWindow = 24 * time.Hour

// Thresholds for deriving a health status.
const (
	// DegradedErrorRate is the share of ERROR events at which a source is called
	// degraded rather than healthy.
	DegradedErrorRate = 0.10

	// DegradedMinEvents keeps a tiny sample from reading as degraded: two events,
	// one of them an error, is not a 50% error rate worth alarming about.
	DegradedMinEvents = 20
)

// HealthStatus is a source's coarse state, ordered by what an operator should
// act on first.
type HealthStatus string

// The set of health statuses.
const (
	// HealthDisconnected means the source was turned off deliberately.
	HealthDisconnected HealthStatus = "disconnected"

	// HealthExpired means the ingest key has lapsed, so the collector is being
	// refused even though the source is still enabled.
	HealthExpired HealthStatus = "expired"

	// HealthNeverConnected means no collector has ever presented this key.
	HealthNeverConnected HealthStatus = "never_connected"

	// HealthSilent means the source has connected before but nothing arrived
	// inside the health window.
	HealthSilent HealthStatus = "silent"

	// HealthDegraded means events are arriving but enough of them are errors to
	// be worth looking at.
	HealthDegraded HealthStatus = "degraded"

	// HealthHealthy means events are arriving and the error rate is ordinary.
	HealthHealthy HealthStatus = "healthy"
)

// Health is a source's state over the health window, derived rather than stored
// so it cannot go stale.
type Health struct {
	Status  HealthStatus
	Events  int64
	Errors  int64
	Dropped int64

	// ErrorRate is Errors/Events, or zero when nothing arrived.
	ErrorRate float64
}

// Health derives a source's health from its counters over the window ending at
// now.
//
// The order of the checks is the point: a disconnected source is not "silent"
// and an expired key is not "healthy" just because events arrived before it
// lapsed. Each state answers a different operator question, so the most
// actionable one wins.
func (s Source) Health(now time.Time, counters HealthCounters) Health {
	h := Health{
		Events:    counters.Events,
		Errors:    counters.Errors,
		Dropped:   counters.Dropped,
		ErrorRate: counters.ErrorRate(),
	}

	switch {
	case !s.IsActive:
		h.Status = HealthDisconnected
	case s.Expired(now):
		h.Status = HealthExpired
	case s.LastSeenAt == nil:
		h.Status = HealthNeverConnected
	case counters.Events == 0:
		h.Status = HealthSilent
	case counters.Events >= DegradedMinEvents && counters.ErrorRate() >= DegradedErrorRate:
		h.Status = HealthDegraded
	default:
		h.Status = HealthHealthy
	}

	return h
}

// HealthCounters is the ingest tally a health decision is made from. It mirrors
// usagebus.SourceCounters, declared here so the source domain does not depend on
// the usage domain for a value type.
type HealthCounters struct {
	Events  int64
	Errors  int64
	Dropped int64
}

// ErrorRate returns the share of events at ERROR, or zero when nothing arrived.
func (c HealthCounters) ErrorRate() float64 {
	if c.Events <= 0 {
		return 0
	}

	return float64(c.Errors) / float64(c.Events)
}
