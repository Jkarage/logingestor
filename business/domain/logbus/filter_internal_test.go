package logbus

import (
	"errors"
	"testing"
	"time"
)

func Test_QueryFilter_scanFilters(t *testing.T) {
	search := "boom"
	source := "api"
	st := "app"

	cases := []struct {
		name string
		f    QueryFilter
		want bool
	}{
		{"empty", QueryFilter{}, false},
		// Everything below became unbounded once its index was built; measured
		// over the full range with values matching nothing: search 12.9ms,
		// source 0.1ms, level 0.1ms, meta 2.2ms.
		{"source_type only", QueryFilter{SourceType: &st}, false},
		{"levels", QueryFilter{Levels: []Level{LevelError}}, false},
		{"search", QueryFilter{Search: &search}, false},
		{"source", QueryFilter{Source: &source}, false},
		{"meta", QueryFilter{Meta: map[string]string{"orderId": "1"}}, false},
		// tags is the exception: no plan is safe for both an absent tag and a
		// common one, so it stays window-bound.
		{"tags", QueryFilter{Tags: []string{"k8s"}}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.scanFilters(); got != c.want {
				t.Errorf("scanFilters() = %v, want %v", got, c.want)
			}
		})
	}
}

func Test_QueryFilter_applyScanWindow(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	search := "boom"

	t.Run("no scan filter leaves the range untouched", func(t *testing.T) {
		st := "app"
		f := QueryFilter{SourceType: &st, Search: &search, Levels: []Level{LevelError}}
		if err := f.applyScanWindow(now); err != nil {
			t.Fatalf("applyScanWindow: %v", err)
		}
		if f.From != nil {
			t.Error("an index-supported filter must not gain a lower bound")
		}
	})

	t.Run("scan filter with no from gets one", func(t *testing.T) {
		f := QueryFilter{Tags: []string{"k8s"}}
		if err := f.applyScanWindow(now); err != nil {
			t.Fatalf("applyScanWindow: %v", err)
		}
		if f.From == nil {
			t.Fatal("expected a lower bound to be applied")
		}
		if got := now.Sub(*f.From); got != MaxRawWindow {
			t.Errorf("window = %v, want %v", got, MaxRawWindow)
		}
	})

	t.Run("window at the cap is allowed", func(t *testing.T) {
		from := now.Add(-MaxRawWindow)
		f := QueryFilter{Tags: []string{"k8s"}, From: &from, To: &now}
		if err := f.applyScanWindow(now); err != nil {
			t.Errorf("a window exactly at the cap should be allowed: %v", err)
		}
	})

	// Measured on the largest project: a non-matching unindexed filter costs
	// ~345ms over two hours but ~4.2s over a day, so a wider range is refused
	// rather than served slowly.
	t.Run("window past the cap is refused", func(t *testing.T) {
		from := now.Add(-MaxRawWindow - time.Minute)
		f := QueryFilter{Tags: []string{"k8s"}, From: &from, To: &now}
		if err := f.applyScanWindow(now); !errors.Is(err, ErrWindowTooWide) {
			t.Errorf("err = %v, want ErrWindowTooWide", err)
		}
	})

	t.Run("explicit to is respected when bounding", func(t *testing.T) {
		to := now.Add(-48 * time.Hour)
		f := QueryFilter{Tags: []string{"k8s"}, To: &to}
		if err := f.applyScanWindow(now); err != nil {
			t.Fatalf("applyScanWindow: %v", err)
		}
		// The window must hang off `to`, not off now, or a historical search
		// silently returns nothing.
		if got := to.Sub(*f.From); got != MaxRawWindow {
			t.Errorf("window = %v, want %v measured back from 'to'", got, MaxRawWindow)
		}
	})
}

func strptr(s string) *string { return &s }
