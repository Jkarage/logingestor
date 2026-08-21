package clienterrordb_test

import (
	"context"
	"testing"
	"time"

	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
)

// spikeConfig is the shape used throughout: a ten minute window against the
// preceding hour, five times the rate, at least 25 events.
func spikeConfig() clienterrorbus.SpikeConfig {
	return clienterrorbus.SpikeConfig{
		Window:     10 * time.Minute,
		Baseline:   time.Hour,
		Multiplier: 5,
		MinEvents:  25,
	}
}

// spikeHarness places events on a timeline the detector will read.
type spikeHarness struct {
	harness
	baselineStart time.Time
	windowStart   time.Time
	windowEnd     time.Time
}

func newSpikeHarness(t *testing.T) spikeHarness {
	t.Helper()

	h := newHarness(t)
	baselineStart, windowStart, windowEnd := clienterrorbus.SpikeBounds(spikeConfig(), time.Now().UTC())

	return spikeHarness{harness: h, baselineStart: baselineStart, windowStart: windowStart, windowEnd: windowEnd}
}

// crashes ingests n events for one issue at a given instant, then groups them.
// The message and frame decide the fingerprint, so the same pair is the same
// issue.
func (h spikeHarness) crashes(t *testing.T, frame string, at time.Time, n int) {
	t.Helper()

	ctx := context.Background()
	who := clienterrorbus.Reporter{OrgID: &h.fixture.OrgID, ProjectID: &h.fixture.ProjectID}

	batch := make([]clienterrorbus.NewEvent, 0, n)
	for i := 0; i < n; i++ {
		e := event("failure in "+frame, frame)
		e.OccurredAt = at
		batch = append(batch, e)
	}

	// Ingest is capped per batch, so a large burst arrives as several.
	for start := 0; start < len(batch); start += clienterrorbus.MaxBatchEvents {
		end := min(start+clienterrorbus.MaxBatchEvents, len(batch))
		if _, err := h.bus.Ingest(ctx, who, batch[start:end]); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}

	for {
		processed, err := h.bus.ProcessBatch(ctx, 200)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if processed == 0 {
			break
		}
	}
}

// backdateIssue moves an issue's first sighting before the window, which is what
// makes it a known issue rather than a new one.
func (h spikeHarness) backdateIssue(t *testing.T, titleLike string, at time.Time) {
	t.Helper()

	if _, err := h.db.DB.Exec(`
		UPDATE client_error_issues SET first_seen_at = $1 WHERE title LIKE $2`, at, "%"+titleLike+"%"); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}
}

// The case the feature exists for: a known issue trickling along, then a deploy
// makes it 40× worse. It is not new and not a regression, so nothing else fires.
func Test_Spike_Integration_DetectsARateJump(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	// Six events spread across the baseline hour: a rate of one per ten minutes.
	for i := 0; i < 6; i++ {
		h.crashes(t, "Checkout", h.baselineStart.Add(time.Duration(i)*10*time.Minute), 1)
	}
	h.backdateIssue(t, "Checkout", h.baselineStart)

	// Nothing yet: the current window is empty.
	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 0 {
		t.Fatalf("found %d spikes with an empty window", len(spikes))
	}

	// Then the deploy.
	h.crashes(t, "Checkout", h.windowStart.Add(time.Minute), 40)

	spikes, err = h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 1 {
		t.Fatalf("found %d spikes, want 1", len(spikes))
	}

	s := spikes[0]
	if s.Current != 40 {
		t.Errorf("current = %d, want 40", s.Current)
	}
	// Six events over an hour scaled to a ten minute window is one.
	if s.Baseline < 0.9 || s.Baseline > 1.1 {
		t.Errorf("baseline = %v, want about 1", s.Baseline)
	}
	if got := s.Multiple(); got < 39 || got > 41 {
		t.Errorf("multiple = %v, want about 40", got)
	}
	if s.Issue.Title == "" || s.Issue.ProjectID == nil {
		t.Errorf("the spike does not carry a usable issue: %+v", s.Issue)
	}
}

// Steady traffic is not a spike, however loud it is. This is the case that
// decides whether the feature is usable or gets muted.
func Test_Spike_Integration_IgnoresSteadyTraffic(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	// 30 events in every ten minute slot of the baseline, and 30 in the window.
	for i := 0; i < 6; i++ {
		h.crashes(t, "Noisy", h.baselineStart.Add(time.Duration(i)*10*time.Minute), 30)
	}
	h.crashes(t, "Noisy", h.windowStart.Add(time.Minute), 30)
	h.backdateIssue(t, "Noisy", h.baselineStart)

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 0 {
		t.Fatalf("steady traffic reported as a spike: %+v", spikes[0])
	}
}

// A tenfold rise on a quiet issue is still nothing worth waking for. The floor
// is what keeps this out.
func Test_Spike_Integration_HonoursTheFloor(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	h.crashes(t, "Quiet", h.baselineStart.Add(time.Minute), 1)
	h.crashes(t, "Quiet", h.windowStart.Add(time.Minute), 10)
	h.backdateIssue(t, "Quiet", h.baselineStart)

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 0 {
		t.Errorf("a 10-event issue tripped the alert: %+v", spikes[0])
	}

	// Lower the floor and the same data does trip it, which shows the floor is
	// what excluded it rather than the ratio.
	cfg := spikeConfig()
	cfg.MinEvents = 5

	spikes, err = h.bus.QuerySpikes(ctx, cfg)
	if err != nil {
		t.Fatalf("query with a lower floor: %v", err)
	}
	if len(spikes) != 1 {
		t.Errorf("found %d spikes with a floor of 5, want 1", len(spikes))
	}
}

// A dormant issue coming back hard has no baseline to be a multiple of, and it
// is exactly the case worth paging for.
func Test_Spike_Integration_DormantIssueBursts(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	// One event long before the baseline period, so the issue is known but its
	// recent rate is zero.
	h.crashes(t, "Dormant", h.baselineStart.Add(-48*time.Hour), 1)
	h.crashes(t, "Dormant", h.windowStart.Add(time.Minute), 200)
	h.backdateIssue(t, "Dormant", h.baselineStart.Add(-48*time.Hour))

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 1 {
		t.Fatalf("found %d spikes, want the dormant issue", len(spikes))
	}
	if spikes[0].Multiple() < 100 {
		t.Errorf("multiple = %v, want it to read as a large jump", spikes[0].Multiple())
	}
}

// A brand-new issue already alerts as new. Alerting again as a spike would be
// the same news twice.
func Test_Spike_Integration_SkipsBrandNewIssues(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	// First seen inside the window, with plenty of events.
	h.crashes(t, "BrandNew", h.windowStart.Add(time.Minute), 100)

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 0 {
		t.Errorf("a brand-new issue was also reported as a spike: %+v", spikes[0])
	}
}

// Ignoring an issue is a standing decision, so it must silence spikes too —
// otherwise the only way to stop hearing about a known-noisy bug is to resolve
// it dishonestly.
func Test_Spike_Integration_RespectsIgnored(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	h.crashes(t, "Ignored", h.baselineStart.Add(time.Minute), 1)
	h.crashes(t, "Ignored", h.windowStart.Add(time.Minute), 100)
	h.backdateIssue(t, "Ignored", h.baselineStart)

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 1 {
		t.Fatalf("found %d spikes before ignoring, want 1", len(spikes))
	}

	ignored := clienterrorbus.StatusIgnored
	if _, err := h.bus.UpdateIssue(ctx, spikes[0].Issue, clienterrorbus.UpdateIssue{Status: &ignored}); err != nil {
		t.Fatalf("ignore: %v", err)
	}

	spikes, err = h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query after ignoring: %v", err)
	}
	if len(spikes) != 0 {
		t.Errorf("an ignored issue still reported a spike")
	}
}

// The evaluator notifies once per detected spike, and re-notification is the
// delivery layer's dedup window rather than anything here.
func Test_Spike_Integration_EvaluatorNotifies(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	h.crashes(t, "Paged", h.baselineStart.Add(time.Minute), 2)
	h.crashes(t, "Paged", h.windowStart.Add(time.Minute), 60)
	h.backdateIssue(t, "Paged", h.baselineStart)

	found, err := h.bus.EvaluateSpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if found != 1 {
		t.Fatalf("evaluated %d spikes, want 1", found)
	}
	if len(h.notes.spiked) != 1 {
		t.Fatalf("notified %d spikes, want 1", len(h.notes.spiked))
	}

	s := h.notes.spiked[0]
	if s.Current != 60 {
		t.Errorf("current = %d, want 60", s.Current)
	}
	if s.Window != 10*time.Minute {
		t.Errorf("window = %v, want the configured one", s.Window)
	}
}

// Two issues spiking at once are two alerts, ordered loudest first so a human
// reading a channel sees the worst one at the top.
func Test_Spike_Integration_OrdersBySize(t *testing.T) {
	h := newSpikeHarness(t)
	ctx := context.Background()

	for _, c := range []struct {
		frame string
		burst int
	}{{"Small", 30}, {"Large", 300}} {
		h.crashes(t, c.frame, h.baselineStart.Add(time.Minute), 1)
		h.crashes(t, c.frame, h.windowStart.Add(time.Minute), c.burst)
		h.backdateIssue(t, c.frame, h.baselineStart)
	}

	spikes, err := h.bus.QuerySpikes(ctx, spikeConfig())
	if err != nil {
		t.Fatalf("query spikes: %v", err)
	}
	if len(spikes) != 2 {
		t.Fatalf("found %d spikes, want 2", len(spikes))
	}
	if spikes[0].Current < spikes[1].Current {
		t.Errorf("spikes are not ordered loudest first: %d then %d", spikes[0].Current, spikes[1].Current)
	}
}

// The window is anchored to a whole multiple of itself so two passes in the same
// window see the same period — otherwise a spike drifts in and out of detection
// as the clock moves.
func Test_SpikeBounds_AreStableWithinAWindow(t *testing.T) {
	cfg := spikeConfig()
	base := time.Date(2026, 8, 21, 10, 3, 27, 0, time.UTC)

	firstBaseline, firstStart, firstEnd := clienterrorbus.SpikeBounds(cfg, base)
	_, laterStart, laterEnd := clienterrorbus.SpikeBounds(cfg, base.Add(90*time.Second))

	if !firstStart.Equal(laterStart) || !firstEnd.Equal(laterEnd) {
		t.Errorf("the window moved within itself: %v-%v then %v-%v", firstStart, firstEnd, laterStart, laterEnd)
	}
	if firstEnd.Sub(firstStart) != cfg.Window {
		t.Errorf("window is %v, want %v", firstEnd.Sub(firstStart), cfg.Window)
	}
	if firstStart.Sub(firstBaseline) != cfg.Baseline {
		t.Errorf("baseline is %v, want %v", firstStart.Sub(firstBaseline), cfg.Baseline)
	}

	// The next window is a clean step, with no gap or overlap.
	_, nextStart, _ := clienterrorbus.SpikeBounds(cfg, base.Add(cfg.Window))
	if !nextStart.Equal(firstEnd) {
		t.Errorf("consecutive windows do not abut: %v then %v", firstEnd, nextStart)
	}
}
