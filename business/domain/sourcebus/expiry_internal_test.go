package sourcebus

import (
	"testing"
	"time"
)

func Test_Source_Expired(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name      string
		expiresAt *time.Time
		want      bool
	}{
		// Nil must mean "never expires" so every source predating migration 1.28
		// keeps working untouched.
		{"nil never expires", nil, false},
		{"future expiry is live", &future, false},
		{"past expiry is dead", &past, true},
		{"exactly at expiry is still live", &now, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Source{ExpiresAt: c.expiresAt}
			if got := s.Expired(now); got != c.want {
				t.Errorf("Expired() = %v, want %v", got, c.want)
			}
		})
	}
}
