package usageapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
)

func req(t *testing.T, query string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "/v1/orgs/x/usage?"+query, nil)
}

func Test_parseWindow_Defaults(t *testing.T) {
	t.Run("no range and no billing period falls back to 30 days", func(t *testing.T) {
		from, to, resp := parseWindow(req(t, ""), nil)
		if resp != nil {
			t.Fatalf("unexpected error response: %v", resp)
		}
		if got := to.Sub(from); got != defaultWindow {
			t.Errorf("window = %v, want %v", got, defaultWindow)
		}
		// The rollup is keyed by day, so the window must reach the end of today
		// or today's ingestion is invisible.
		if !to.After(time.Now().UTC()) {
			t.Errorf("to = %v should be past now so today is included", to)
		}
	})

	t.Run("billing period start is preferred as the default from", func(t *testing.T) {
		start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		from, _, resp := parseWindow(req(t, ""), &start)
		if resp != nil {
			t.Fatalf("unexpected error response: %v", resp)
		}
		if !from.Equal(start) {
			t.Errorf("from = %v, want the period start %v", from, start)
		}
	})

	t.Run("explicit values override the defaults", func(t *testing.T) {
		start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		from, to, resp := parseWindow(req(t, "from=2026-07-01T00:00:00Z&to=2026-07-15T00:00:00Z"), &start)
		if resp != nil {
			t.Fatalf("unexpected error response: %v", resp)
		}
		if from.Month() != time.July || to.Day() != 15 {
			t.Errorf("from = %v, to = %v; explicit values should win over the period", from, to)
		}
	})
}

func Test_parseWindow_Rejections(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"unparseable from", "from=yesterday"},
		{"unparseable to", "to=2026-13-45"},
		{"inverted range", "from=2026-08-10T00:00:00Z&to=2026-08-01T00:00:00Z"},
		{"equal bounds", "from=2026-08-10T00:00:00Z&to=2026-08-10T00:00:00Z"},
		{"window too wide", "from=2000-01-01T00:00:00Z&to=2026-01-01T00:00:00Z"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, resp := parseWindow(req(t, c.query), nil)
			if resp == nil {
				t.Fatal("expected an error response")
			}
			e, ok := resp.(*errs.Error)
			if !ok || e.Code != errs.InvalidArgument {
				t.Errorf("got %T %v, want InvalidArgument", resp, resp)
			}
		})
	}
}
