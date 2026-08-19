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
		// source_type has a composite index, so it stays unbounded.
		{"source_type only", QueryFilter{SourceType: &st}, false},
		// levels does not: `level = ANY(...)` cannot use logs_level_idx and ran
		// past 60s unbounded on the largest project.
		{"levels", QueryFilter{Levels: []Level{LevelError}}, true},
		{"search", QueryFilter{Search: &search}, true},
		{"source", QueryFilter{Source: &source}, true},
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
		f := QueryFilter{SourceType: &st}
		if err := f.applyScanWindow(now); err != nil {
			t.Fatalf("applyScanWindow: %v", err)
		}
		if f.From != nil {
			t.Error("an index-supported filter must not gain a lower bound")
		}
	})

	t.Run("scan filter with no from gets one", func(t *testing.T) {
		f := QueryFilter{Search: &search}
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
		f := QueryFilter{Search: &search, From: &from, To: &now}
		if err := f.applyScanWindow(now); err != nil {
			t.Errorf("a window exactly at the cap should be allowed: %v", err)
		}
	})

	// Measured on the largest project: a non-matching unindexed filter costs
	// ~345ms over two hours but ~4.2s over a day, so a wider range is refused
	// rather than served slowly.
	t.Run("window past the cap is refused", func(t *testing.T) {
		from := now.Add(-MaxRawWindow - time.Minute)
		f := QueryFilter{Search: &search, From: &from, To: &now}
		if err := f.applyScanWindow(now); !errors.Is(err, ErrWindowTooWide) {
			t.Errorf("err = %v, want ErrWindowTooWide", err)
		}
	})

	t.Run("explicit to is respected when bounding", func(t *testing.T) {
		to := now.Add(-48 * time.Hour)
		f := QueryFilter{Source: strptr("api"), To: &to}
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
