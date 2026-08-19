package integrationbus

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testNow  = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	testProj = uuid.New()
)

func firing(lastNotified *time.Time) *AlertEvent {
	return &AlertEvent{State: AlertStateFiring, LastNotifiedAt: lastNotified}
}

func ago(d time.Duration) *time.Time { t := testNow.Add(-d); return &t }

func base() SuppressInput {
	return SuppressInput{Now: testNow, RuleActive: true, DedupWindow: 5 * time.Minute, ProjectID: testProj}
}

func Test_ShouldNotify_NewAlertAlwaysNotifies(t *testing.T) {
	in := base()
	if ok, why := ShouldNotify(in); !ok {
		t.Errorf("a brand new alert must notify, got suppressed by %q", why)
	}

	// A previously resolved alert firing again is a new incident.
	in.Existing = &AlertEvent{State: AlertStateResolved, LastNotifiedAt: ago(time.Second)}
	if ok, why := ShouldNotify(in); !ok {
		t.Errorf("a re-fire after resolve must notify, got %q", why)
	}
}

func Test_ShouldNotify_Dedup(t *testing.T) {
	t.Run("inside the window folds into the open alert", func(t *testing.T) {
		in := base()
		in.Existing = firing(ago(time.Minute))
		if ok, why := ShouldNotify(in); ok || why != SuppressDedup {
			t.Errorf("ok=%v why=%q, want suppressed by dedup", ok, why)
		}
	})

	// A persistent problem must page again eventually, or it goes unnoticed.
	t.Run("past the window re-notifies", func(t *testing.T) {
		in := base()
		in.Existing = firing(ago(6 * time.Minute))
		if ok, _ := ShouldNotify(in); !ok {
			t.Error("expected a re-notify once the dedup window elapsed")
		}
	})

	t.Run("exactly at the window re-notifies", func(t *testing.T) {
		in := base()
		in.Existing = firing(ago(5 * time.Minute))
		if ok, _ := ShouldNotify(in); !ok {
			t.Error("the boundary should notify, not suppress")
		}
	})

	// An open alert that was never delivered must not be silenced by dedup, or a
	// failed first delivery would never be retried.
	t.Run("open but never notified", func(t *testing.T) {
		in := base()
		in.Existing = firing(nil)
		if ok, _ := ShouldNotify(in); !ok {
			t.Error("an open alert with no notification yet must notify")
		}
	})

	t.Run("zero window falls back to the default", func(t *testing.T) {
		in := base()
		in.DedupWindow = 0
		in.Existing = firing(ago(DefaultDedupWindow - time.Minute))
		if ok, why := ShouldNotify(in); ok || why != SuppressDedup {
			t.Errorf("ok=%v why=%q, want the default window applied", ok, why)
		}
	})
}

func Test_ShouldNotify_Precedence(t *testing.T) {
	// Each case would otherwise notify; the listed condition must win.
	cases := []struct {
		name   string
		mutate func(*SuppressInput)
		want   SuppressReason
	}{
		{
			name:   "inactive rule beats everything",
			mutate: func(in *SuppressInput) { in.RuleActive = false },
			want:   SuppressInactive,
		},
		{
			name: "snooze beats maintenance and dedup",
			mutate: func(in *SuppressInput) {
				until := testNow.Add(time.Hour)
				in.SnoozeUntil = &until
				in.Maintenances = []MaintenanceWindow{{StartsAt: testNow.Add(-time.Hour), EndsAt: testNow.Add(time.Hour)}}
			},
			want: SuppressSnoozed,
		},
		{
			name: "maintenance beats acknowledged and dedup",
			mutate: func(in *SuppressInput) {
				in.Maintenances = []MaintenanceWindow{{StartsAt: testNow.Add(-time.Hour), EndsAt: testNow.Add(time.Hour)}}
				in.Existing = &AlertEvent{State: AlertStateAcknowledged}
			},
			want: SuppressMaintenance,
		},
		{
			name: "acknowledged beats dedup",
			mutate: func(in *SuppressInput) {
				in.Existing = &AlertEvent{State: AlertStateAcknowledged, LastNotifiedAt: ago(time.Hour)}
			},
			want: SuppressAcknowledged,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mutate(&in)
			ok, why := ShouldNotify(in)
			if ok {
				t.Fatalf("expected suppression by %q, but it notified", c.want)
			}
			if why != c.want {
				t.Errorf("why = %q, want %q", why, c.want)
			}
		})
	}
}

func Test_ShouldNotify_ExpiredSnoozeAndWindow(t *testing.T) {
	t.Run("a snooze in the past no longer suppresses", func(t *testing.T) {
		in := base()
		until := testNow.Add(-time.Second)
		in.SnoozeUntil = &until
		if ok, why := ShouldNotify(in); !ok {
			t.Errorf("an elapsed snooze must not suppress, got %q", why)
		}
	})

	t.Run("a maintenance window that has ended no longer suppresses", func(t *testing.T) {
		in := base()
		in.Maintenances = []MaintenanceWindow{{StartsAt: testNow.Add(-2 * time.Hour), EndsAt: testNow.Add(-time.Hour)}}
		if ok, why := ShouldNotify(in); !ok {
			t.Errorf("a past window must not suppress, got %q", why)
		}
	})
}

func Test_MaintenanceWindow_Covers(t *testing.T) {
	other := uuid.New()

	orgWide := MaintenanceWindow{StartsAt: testNow.Add(-time.Hour), EndsAt: testNow.Add(time.Hour)}
	if !orgWide.Covers(testProj, testNow) || !orgWide.Covers(other, testNow) {
		t.Error("a nil ProjectID window must cover every project in the org")
	}

	scoped := orgWide
	scoped.ProjectID = &testProj
	if !scoped.Covers(testProj, testNow) {
		t.Error("a scoped window must cover its own project")
	}
	if scoped.Covers(other, testNow) {
		t.Error("a scoped window must not cover another project")
	}

	// Half-open, so back-to-back windows neither overlap nor leave a gap.
	if !orgWide.Covers(testProj, orgWide.StartsAt) {
		t.Error("the start instant is inside the window")
	}
	if orgWide.Covers(testProj, orgWide.EndsAt) {
		t.Error("the end instant is outside the window")
	}
	if orgWide.Covers(testProj, orgWide.StartsAt.Add(-time.Nanosecond)) {
		t.Error("before the start is outside the window")
	}
}
