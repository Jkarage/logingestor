package usagebus

import (
	"testing"
)

func Test_QuotaStatus_Exceeded(t *testing.T) {
	cases := []struct {
		name  string
		quota int64
		used  int64
		want  bool
	}{
		// -1 is the unlimited sentinel; any usage must stay under it.
		{"unlimited is never exceeded", -1, 1 << 40, false},
		{"under quota", 1000, 999, false},
		{"exactly at quota is exceeded", 1000, 1000, true},
		{"over quota", 1000, 1001, true},
		{"zero quota admits nothing", 0, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QuotaStatus{Quota: c.quota, Used: c.used}
			if got := q.Exceeded(); got != c.want {
				t.Errorf("Exceeded() = %v, want %v", got, c.want)
			}
		})
	}
}
