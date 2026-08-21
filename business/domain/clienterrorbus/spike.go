package clienterrorbus

import (
	"context"
	"fmt"
	"time"
)

// Spike is an issue whose rate has jumped.
type Spike struct {
	Issue Issue

	// Current is how many events landed in the window.
	Current int64

	// Baseline is the per-window rate before it, scaled to the window's length so
	// the two numbers are comparable.
	Baseline float64

	// Window is the period Current covers.
	Window time.Duration
}

// Multiple returns how many times the baseline the current rate is. A dormant
// issue has no baseline to divide by, so the floor stands in — the alert then
// reads as a large multiple, which is what a jump from nothing is.
func (s Spike) Multiple() float64 {
	if s.Baseline < 1 {
		return float64(s.Current)
	}

	return float64(s.Current) / s.Baseline
}

// SpikeConfig is what counts as a spike.
//
// The shape is relative with a floor, which is what people mean by "spiking":
// an absolute threshold has to be set per issue to be useful, and nobody sets
// it. The floor is what keeps a quiet issue going from one event an hour to ten
// — a tenfold rise, and nothing worth waking for — out of the alerts.
type SpikeConfig struct {
	// Window is the period the current rate is measured over.
	Window time.Duration

	// Baseline is how far back the comparison rate is drawn from, ending where
	// the window begins.
	Baseline time.Duration

	// Multiplier is how many times the baseline rate counts as a spike.
	Multiplier float64

	// MinEvents is the floor: fewer than this in the window is never a spike,
	// whatever the ratio.
	MinEvents int
}

// Defaults for spike detection. Ten minutes is short enough to catch a bad
// deploy while it is still rolling out and long enough that a single unlucky
// minute does not read as a trend.
const (
	DefaultSpikeWindow     = 10 * time.Minute
	DefaultSpikeBaseline   = time.Hour
	DefaultSpikeMultiplier = 5.0
	DefaultSpikeMinEvents  = 25
)

func (c SpikeConfig) withDefaults() SpikeConfig {
	if c.Window <= 0 {
		c.Window = DefaultSpikeWindow
	}
	if c.Baseline < c.Window {
		// A baseline shorter than the window cannot describe a rate the window is
		// compared against.
		c.Baseline = DefaultSpikeBaseline
	}
	if c.Multiplier <= 1 {
		c.Multiplier = DefaultSpikeMultiplier
	}
	if c.MinEvents <= 0 {
		c.MinEvents = DefaultSpikeMinEvents
	}

	return c
}

// QuerySpikes returns the issues whose rate has jumped, without notifying. It is
// what the evaluator reads, and what a dashboard would read to badge an issue as
// spiking rather than waiting for an alert to arrive.
func (b *Business) QuerySpikes(ctx context.Context, cfg SpikeConfig) ([]Spike, error) {
	spikes, err := b.storer.QuerySpikes(ctx, cfg.withDefaults(), time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("queryspikes: %w", err)
	}

	return spikes, nil
}

// EvaluateSpikes finds issues whose rate has jumped and notifies each once.
//
// It runs on a timer for the same reason threshold rules do: a rate is a
// property of a period, not of the event that happens to be the hundredth. An
// issue that stays elevated is not re-notified on every pass — the dedup window
// in the delivery path governs that, exactly as it does for every other alert.
func (b *Business) EvaluateSpikes(ctx context.Context, cfg SpikeConfig) (int, error) {
	spikes, err := b.QuerySpikes(ctx, cfg)
	if err != nil {
		return 0, err
	}

	if b.notifier == nil {
		return len(spikes), nil
	}

	for _, s := range spikes {
		b.notifier.IssueSpiked(ctx, s.Issue, s)
	}

	return len(spikes), nil
}

// spikeBounds returns the three instants the query needs: where the baseline
// starts, where the window starts, and where it ends.
//
// The window ends on a whole multiple of itself rather than at "now" so
// consecutive passes look at the same period and produce the same answer —
// otherwise a spike drifts in and out of detection as the clock moves.
func spikeBounds(cfg SpikeConfig, now time.Time) (baselineStart, windowStart, windowEnd time.Time) {
	windowEnd = now.Truncate(cfg.Window)
	windowStart = windowEnd.Add(-cfg.Window)
	baselineStart = windowStart.Add(-cfg.Baseline)

	return baselineStart, windowStart, windowEnd
}

// SpikeBounds exposes the window arithmetic to the store, which needs the same
// three instants, and to tests, which need to place events inside them.
func SpikeBounds(cfg SpikeConfig, now time.Time) (baselineStart, windowStart, windowEnd time.Time) {
	return spikeBounds(cfg.withDefaults(), now)
}
