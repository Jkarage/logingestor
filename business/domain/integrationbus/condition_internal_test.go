package integrationbus

import (
	"errors"
	"testing"
	"time"
)

func Test_Query_Matches(t *testing.T) {
	// An empty query must match nothing. A rule that fires on every log is
	// almost never intended, and silently doing so would be an alert storm.
	t.Run("empty query matches nothing", func(t *testing.T) {
		if (Query{}).Matches("ERROR", "anything", "api") {
			t.Error("empty query should not match")
		}
	})

	q := Query{Levels: []string{"ERROR", "WARN"}, Contains: "timeout", Source: "api"}

	t.Run("all predicates must hold", func(t *testing.T) {
		if !q.Matches("ERROR", "upstream TIMEOUT reached", "api") {
			t.Error("expected a match when every predicate holds")
		}
		// Each of these fails exactly one predicate.
		for name, args := range map[string][3]string{
			"level excluded":   {"INFO", "upstream timeout reached", "api"},
			"text absent":      {"ERROR", "upstream refused", "api"},
			"source different": {"ERROR", "upstream timeout reached", "worker"},
		} {
			if q.Matches(args[0], args[1], args[2]) {
				t.Errorf("%s should not match", name)
			}
		}
	})

	t.Run("text and level and source are case-insensitive", func(t *testing.T) {
		if !q.Matches("error", "TIMEOUT", "API") {
			t.Error("matching should not depend on case")
		}
	})

	t.Run("a single predicate is enough", func(t *testing.T) {
		if !(Query{Contains: "boom"}).Matches("INFO", "kaBOOMed", "anything") {
			t.Error("contains-only query should match")
		}
		if !(Query{Source: "api"}).Matches("INFO", "whatever", "api") {
			t.Error("source-only query should match")
		}
	})
}

func Test_Condition_Validate(t *testing.T) {
	good := map[string]Condition{
		"level": LevelCondition("ERROR"),
		"match": {Type: ConditionMatch, Query: &Query{Contains: "boom"}},
		"threshold": {
			Type: ConditionThreshold, Query: &Query{Levels: []string{"ERROR"}},
			WindowSeconds: 300, Count: 10, Comparator: ComparatorGTE,
		},
	}
	for name, c := range good {
		if err := c.Validate(); err != nil {
			t.Errorf("%s condition rejected: %v", name, err)
		}
	}

	bad := []struct {
		name string
		c    Condition
		want error
	}{
		{"unknown type", Condition{Type: "magic"}, ErrConditionType},
		{"empty type", Condition{}, ErrConditionType},
		{"level missing", Condition{Type: ConditionLevel}, ErrConditionLevel},
		{"level bogus", LevelCondition("CATASTROPHE"), ErrConditionLevel},
		{"match without query", Condition{Type: ConditionMatch}, ErrConditionQuery},
		{"match with empty query", Condition{Type: ConditionMatch, Query: &Query{}}, ErrConditionQuery},
		{"match with bad level", Condition{Type: ConditionMatch, Query: &Query{Levels: []string{"NOPE"}}}, ErrConditionLevel},
		{
			"threshold window too short",
			Condition{Type: ConditionThreshold, Query: &Query{Contains: "x"}, WindowSeconds: 5, Count: 1, Comparator: ComparatorGTE},
			ErrConditionWindow,
		},
		{
			// An unbounded window would make the evaluator scan a whole project.
			"threshold window too long",
			Condition{Type: ConditionThreshold, Query: &Query{Contains: "x"}, WindowSeconds: int((25 * time.Hour).Seconds()), Count: 1, Comparator: ComparatorGTE},
			ErrConditionWindow,
		},
		{
			"threshold count zero",
			Condition{Type: ConditionThreshold, Query: &Query{Contains: "x"}, WindowSeconds: 300, Count: 0, Comparator: ComparatorGTE},
			ErrConditionCount,
		},
		{
			"threshold bad comparator",
			Condition{Type: ConditionThreshold, Query: &Query{Contains: "x"}, WindowSeconds: 300, Count: 5, Comparator: "roughly"},
			ErrConditionComparator,
		},
	}

	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if err := c.c.Validate(); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// A level condition must keep firing at or above its severity, which is what the
// pre-existing rules did and what migration 1.34 backfilled them to.
func Test_Condition_MatchesLog_LevelIsAtOrAbove(t *testing.T) {
	c := LevelCondition("WARN")

	for _, lvl := range []string{"WARN", "ERROR"} {
		if !c.MatchesLog(lvl, "m", "s") {
			t.Errorf("%s should match a WARN rule", lvl)
		}
	}
	for _, lvl := range []string{"DEBUG", "INFO"} {
		if c.MatchesLog(lvl, "m", "s") {
			t.Errorf("%s should not match a WARN rule", lvl)
		}
	}
	if c.MatchesLog("NONSENSE", "m", "s") {
		t.Error("an unknown level must not match")
	}
}

func Test_Condition_Satisfied(t *testing.T) {
	gte := Condition{Type: ConditionThreshold, Count: 10, Comparator: ComparatorGTE}
	gt := Condition{Type: ConditionThreshold, Count: 10, Comparator: ComparatorGT}

	// The boundary is the whole point of having two comparators.
	if !gte.Satisfied(10) || gte.Satisfied(9) {
		t.Error("gte must fire at exactly the count and not below")
	}
	if gt.Satisfied(10) || !gt.Satisfied(11) {
		t.Error("gt must fire only above the count")
	}

	// Non-threshold conditions fire on any match.
	if !LevelCondition("ERROR").Satisfied(1) || LevelCondition("ERROR").Satisfied(0) {
		t.Error("a non-threshold condition should fire on any match")
	}
}

func Test_Condition_DedupKey(t *testing.T) {
	// Level and match rules collapse per level, so an ERROR storm is one alert
	// while a WARN alongside it stays distinct.
	lvl := LevelCondition("WARN")
	if lvl.DedupKey("error") != "ERROR" {
		t.Errorf("got %q", lvl.DedupKey("error"))
	}
	if lvl.DedupKey("ERROR") == lvl.DedupKey("WARN") {
		t.Error("different levels must not share a dedup key")
	}

	// A threshold rule reports the window as one fact, so the level it happened
	// to see must not split it into separate alerts.
	th := Condition{Type: ConditionThreshold}
	if th.DedupKey("ERROR") != th.DedupKey("WARN") {
		t.Error("a threshold rule must dedup to a single key")
	}
}

func Test_Condition_NeedsEvaluator(t *testing.T) {
	if LevelCondition("ERROR").NeedsEvaluator() {
		t.Error("a level rule is decidable from one log")
	}
	if (Condition{Type: ConditionMatch, Query: &Query{Contains: "x"}}).NeedsEvaluator() {
		t.Error("a match rule is decidable from one log")
	}
	if !(Condition{Type: ConditionThreshold}).NeedsEvaluator() {
		t.Error("a threshold rule needs windowed counting")
	}
}

func Test_Condition_RoundTrip(t *testing.T) {
	in := Condition{
		Type: ConditionThreshold, Query: &Query{Levels: []string{"ERROR"}, Contains: "timeout"},
		WindowSeconds: 300, Count: 10, Comparator: ComparatorGTE,
	}

	data, err := MarshalCondition(in)
	if err != nil {
		t.Fatalf("MarshalCondition: %v", err)
	}

	out, err := ParseCondition(data)
	if err != nil {
		t.Fatalf("ParseCondition: %v", err)
	}
	if out.Type != in.Type || out.Count != in.Count || out.WindowSeconds != in.WindowSeconds {
		t.Errorf("round trip lost fields: %+v", out)
	}
	if out.Query == nil || out.Query.Contains != "timeout" {
		t.Errorf("round trip lost the query: %+v", out.Query)
	}

	// The shape migration 1.34 writes must parse.
	if _, err := ParseCondition([]byte(`{"type":"level","level":"ERROR"}`)); err != nil {
		t.Errorf("the backfilled condition shape must parse: %v", err)
	}

	// A malformed stored row must error rather than yield a rule that never fires.
	for _, bad := range []string{`{`, `{"type":"level"}`, `{"type":"nope"}`, `null`} {
		if _, err := ParseCondition([]byte(bad)); err == nil {
			t.Errorf("ParseCondition(%q) should have failed", bad)
		}
	}
}
