package logapp

import (
	"net/url"
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

func Test_metaFilters(t *testing.T) {
	parse := func(qs string) map[string]string {
		v, err := url.ParseQuery(qs)
		if err != nil {
			t.Fatalf("bad fixture %q: %v", qs, err)
		}
		return metaFilters(v)
	}

	t.Run("collects every meta pair", func(t *testing.T) {
		got := parse("meta.orderId=123&meta.traceId=abc&level=ERROR")
		want := map[string]string{"orderId": "123", "traceId": "abc"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no meta params yields nil", func(t *testing.T) {
		if got := parse("level=ERROR&q=boom"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	// Ignored rather than rejected, so an unrelated future "meta…" parameter
	// cannot break callers that already work.
	t.Run("ignores unusable keys and blank values", func(t *testing.T) {
		for _, qs := range []string{
			"meta.=123",           // no field name
			"metadata=123",        // not the meta. prefix
			"meta.orderId=",       // blank value
			"meta.orderId=%20%20", // whitespace-only value
			"meta.bad+key=1",      // space in field name
			`meta.a%22b=1`,        // quote in field name
		} {
			if got := parse(qs); got != nil {
				t.Errorf("parse(%q) = %v, want nil", qs, got)
			}
		}
	})

	t.Run("trims the value", func(t *testing.T) {
		if got := parse("meta.orderId=%20123%20"); got["orderId"] != "123" {
			t.Errorf("got %q, want 123", got["orderId"])
		}
	})
}
