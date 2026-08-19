package logapp

import (
	"reflect"
	"testing"
)

func Test_csvValues(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"repeated params", []string{"WARN", "ERROR"}, []string{"WARN", "ERROR"}},
		{"comma separated", []string{"WARN,ERROR"}, []string{"WARN", "ERROR"}},
		{"both forms mixed", []string{"a", "b,c"}, []string{"a", "b", "c"}},
		{"blanks and spacing dropped", []string{" a , ,b ", ""}, []string{"a", "b"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := csvValues(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("csvValues(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func Test_firstNonEmpty(t *testing.T) {
	// q wins over search when both are sent; whitespace-only counts as absent.
	if got := firstNonEmpty("q-val", "search-val"); got != "q-val" {
		t.Errorf("got %q, want q-val", got)
	}
	if got := firstNonEmpty("", "search-val"); got != "search-val" {
		t.Errorf("got %q, want search-val", got)
	}
	if got := firstNonEmpty("   ", "search-val"); got != "search-val" {
		t.Errorf("got %q, want search-val", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
