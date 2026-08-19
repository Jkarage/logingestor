package integrationbus

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Alert condition kinds.
//
// level reproduces the original behaviour: fire when a log is at or above a
// severity. match fires on any log satisfying a predicate. threshold fires when
// the predicate matches often enough inside a window, which is the one kind that
// cannot be decided from a single log.
const (
	ConditionLevel     = "level"
	ConditionMatch     = "match"
	ConditionThreshold = "threshold"
)

// Comparators for a threshold condition.
const (
	ComparatorGTE = "gte"
	ComparatorGT  = "gt"
)

// Bounds on a threshold window. The evaluator counts from logs over this range,
// so it is kept short: an unbounded window would scan a whole project.
const (
	MinThresholdWindow = 30 * time.Second
	MaxThresholdWindow = time.Hour
	MaxThresholdCount  = 1_000_000
)

// Errors returned when validating a condition.
var (
	ErrConditionType       = fmt.Errorf("condition type must be %q, %q, or %q", ConditionLevel, ConditionMatch, ConditionThreshold)
	ErrConditionLevel      = errors.New("a level condition needs a level of DEBUG, INFO, WARN, or ERROR")
	ErrConditionQuery      = errors.New("a match or threshold condition needs a query with at least one predicate")
	ErrConditionWindow     = fmt.Errorf("threshold windowSeconds must be between %d and %d", int(MinThresholdWindow.Seconds()), int(MaxThresholdWindow.Seconds()))
	ErrConditionCount      = errors.New("threshold count must be a positive number")
	ErrConditionComparator = fmt.Errorf("threshold comparator must be %q or %q", ComparatorGTE, ComparatorGT)
)

// Query is the predicate shared by match and threshold conditions. An empty
// Query matches nothing: a rule that fires on everything is almost never what
// was meant, and is better expressed as a level condition.
type Query struct {
	Levels   []string `json:"levels,omitempty"`
	Contains string   `json:"contains,omitempty"`
	Source   string   `json:"source,omitempty"`
}

// IsZero reports whether the query carries no predicate at all.
func (q Query) IsZero() bool {
	return len(q.Levels) == 0 && strings.TrimSpace(q.Contains) == "" && strings.TrimSpace(q.Source) == ""
}

// Matches reports whether one log satisfies the query. Every populated field
// must match, so adding a predicate always narrows.
func (q Query) Matches(level, message, source string) bool {
	if q.IsZero() {
		return false
	}

	if len(q.Levels) > 0 {
		var ok bool
		for _, l := range q.Levels {
			if strings.EqualFold(l, level) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if c := strings.TrimSpace(q.Contains); c != "" {
		if !strings.Contains(strings.ToLower(message), strings.ToLower(c)) {
			return false
		}
	}

	if s := strings.TrimSpace(q.Source); s != "" && !strings.EqualFold(s, source) {
		return false
	}

	return true
}

// Condition describes when a rule fires.
type Condition struct {
	Type string `json:"type"`

	// Level applies to a level condition.
	Level string `json:"level,omitempty"`

	// Query applies to match and threshold conditions.
	Query *Query `json:"query,omitempty"`

	// WindowSeconds, Count and Comparator apply to a threshold condition.
	WindowSeconds int    `json:"windowSeconds,omitempty"`
	Count         int    `json:"count,omitempty"`
	Comparator    string `json:"comparator,omitempty"`
}

// LevelCondition builds the condition equivalent to a legacy level rule.
func LevelCondition(level string) Condition {
	return Condition{Type: ConditionLevel, Level: level}
}

// Window returns a threshold condition's window as a duration.
func (c Condition) Window() time.Duration {
	return time.Duration(c.WindowSeconds) * time.Second
}

// NeedsEvaluator reports whether deciding this condition requires counting over
// a window rather than inspecting a single log. Those rules are driven by the
// periodic evaluator, not the ingest path.
func (c Condition) NeedsEvaluator() bool {
	return c.Type == ConditionThreshold
}

// Validate checks a condition submitted through the API.
func (c Condition) Validate() error {
	switch c.Type {
	case ConditionLevel:
		if severityRank(c.Level) < 0 {
			return ErrConditionLevel
		}
		return nil

	case ConditionMatch:
		if c.Query == nil || c.Query.IsZero() {
			return ErrConditionQuery
		}
		return c.validateQueryLevels()

	case ConditionThreshold:
		if c.Query == nil || c.Query.IsZero() {
			return ErrConditionQuery
		}
		if err := c.validateQueryLevels(); err != nil {
			return err
		}
		if w := c.Window(); w < MinThresholdWindow || w > MaxThresholdWindow {
			return ErrConditionWindow
		}
		if c.Count <= 0 || c.Count > MaxThresholdCount {
			return ErrConditionCount
		}
		switch c.Comparator {
		case ComparatorGTE, ComparatorGT:
		default:
			return ErrConditionComparator
		}
		return nil
	}

	return ErrConditionType
}

func (c Condition) validateQueryLevels() error {
	for _, l := range c.Query.Levels {
		if severityRank(l) < 0 {
			return ErrConditionLevel
		}
	}
	return nil
}

// MatchesLog reports whether a single log satisfies the condition. Threshold
// conditions return the predicate result only — whether the count is reached is
// the evaluator's decision, not this function's.
func (c Condition) MatchesLog(level, message, source string) bool {
	switch c.Type {
	case ConditionLevel:
		r, want := severityRank(level), severityRank(c.Level)
		return r >= 0 && want >= 0 && r >= want

	case ConditionMatch, ConditionThreshold:
		if c.Query == nil {
			return false
		}
		return c.Query.Matches(level, message, source)
	}

	return false
}

// Satisfied reports whether a threshold condition's count has been reached.
func (c Condition) Satisfied(matched int) bool {
	if c.Type != ConditionThreshold {
		return matched > 0
	}

	if c.Comparator == ComparatorGT {
		return matched > c.Count
	}

	return matched >= c.Count
}

// DedupKey identifies the alert a firing belongs to, so repeats collapse onto
// one open event instead of notifying per log. Level and match rules dedup per
// (rule, level); threshold rules dedup per rule, since the whole window is the
// single fact being reported.
func (c Condition) DedupKey(level string) string {
	if c.Type == ConditionThreshold {
		return "threshold"
	}
	return strings.ToUpper(strings.TrimSpace(level))
}

// MarshalCondition encodes a condition for storage.
func MarshalCondition(c Condition) ([]byte, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal condition: %w", err)
	}
	return data, nil
}

// ParseCondition decodes a stored condition and validates it, so a malformed row
// surfaces as an error rather than a rule that silently never fires.
func ParseCondition(data []byte) (Condition, error) {
	var c Condition
	if err := json.Unmarshal(data, &c); err != nil {
		return Condition{}, fmt.Errorf("unmarshal condition: %w", err)
	}

	if err := c.Validate(); err != nil {
		return Condition{}, err
	}

	return c, nil
}
