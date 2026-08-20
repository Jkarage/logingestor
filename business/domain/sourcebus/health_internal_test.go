package sourcebus

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func Test_Source_Health(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	seen := now.Add(-10 * time.Minute)
	lapsed := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	active := Source{ID: uuid.New(), IsActive: true, LastSeenAt: &seen}

	cases := []struct {
		name     string
		source   Source
		counters HealthCounters
		want     HealthStatus
	}{
		{
			name:     "busy source with few errors is healthy",
			source:   active,
			counters: HealthCounters{Events: 1000, Errors: 3},
			want:     HealthHealthy,
		},
		{
			name:     "an error rate at the threshold is degraded",
			source:   active,
			counters: HealthCounters{Events: 100, Errors: 10},
			want:     HealthDegraded,
		},
		{
			name:     "a tiny sample is not degraded on one error",
			source:   active,
			counters: HealthCounters{Events: 2, Errors: 1},
			want:     HealthHealthy,
		},
		{
			name:     "nothing in the window is silent",
			source:   active,
			counters: HealthCounters{},
			want:     HealthSilent,
		},
		{
			name:     "a source that never connected is not merely silent",
			source:   Source{IsActive: true},
			counters: HealthCounters{},
			want:     HealthNeverConnected,
		},
		{
			// Events arrived before the key lapsed, so the counters look fine;
			// the collector is being refused right now, which is what matters.
			name:     "an expired key outranks arriving events",
			source:   Source{IsActive: true, LastSeenAt: &seen, ExpiresAt: &lapsed},
			counters: HealthCounters{Events: 1000, Errors: 0},
			want:     HealthExpired,
		},
		{
			name:     "a key expiring later is not expired",
			source:   Source{IsActive: true, LastSeenAt: &seen, ExpiresAt: &future},
			counters: HealthCounters{Events: 10, Errors: 0},
			want:     HealthHealthy,
		},
		{
			name:     "a disconnected source reports as disconnected, not expired",
			source:   Source{IsActive: false, LastSeenAt: &seen, ExpiresAt: &lapsed},
			counters: HealthCounters{},
			want:     HealthDisconnected,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.source.Health(now, c.counters)

			if got.Status != c.want {
				t.Errorf("status = %q, want %q", got.Status, c.want)
			}
			if got.Events != c.counters.Events || got.Errors != c.counters.Errors {
				t.Errorf("counters = %d/%d, want %d/%d", got.Events, got.Errors, c.counters.Events, c.counters.Errors)
			}
		})
	}
}

func Test_HealthCounters_ErrorRate(t *testing.T) {
	if got := (HealthCounters{}).ErrorRate(); got != 0 {
		t.Errorf("empty error rate = %v, want 0", got)
	}
	if got := (HealthCounters{Events: 4, Errors: 1}).ErrorRate(); got != 0.25 {
		t.Errorf("error rate = %v, want 0.25", got)
	}
}
