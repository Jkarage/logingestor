package ingest

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jkarage/logingestor/business/domain/logbus"
)

// MaxFutureSkew bounds how far ahead of "now" a timestamp may be before it is
// clamped to the receive time. Logs from clock-skewed agents shouldn't land in
// the future, but reasonable backfill (timestamps in the past) is allowed.
const MaxFutureSkew = 24 * time.Hour

// Record is the normalized, protocol-agnostic shape every listener produces.
// Normalize mutates it in place; the listener then converts it to logbus.NewLog.
type Record struct {
	Level      logbus.Level
	Message    string
	Source     string
	Timestamp  time.Time
	Tags       []string
	Infra      logbus.Infra
	Attributes map[string]any
}

// Normalize applies the shared pipeline steps to rec: format-sniff structured
// fields out of the message, clamp the timestamp, and redact secrets. Severity
// mapping is protocol-specific and must be done by the caller before this.
func Normalize(rec *Record, now time.Time, red *Redactor) {
	if rec.Attributes == nil {
		rec.Attributes = map[string]any{}
	}

	// Lift structured fields out of a JSON or logfmt message into attributes
	// without clobbering explicitly-supplied ones.
	if fields, ok := SniffMessage(rec.Message); ok {
		for k, v := range fields {
			if _, exists := rec.Attributes[k]; !exists {
				rec.Attributes[k] = v
			}
		}
	}

	rec.Timestamp = NormalizeTimestamp(rec.Timestamp, now)

	if red != nil {
		rec.Message = red.RedactString(rec.Message)
		red.RedactMap(rec.Attributes)
	}
}

// NormalizeTimestamp returns ts in UTC, clamping it to now when it is zero or
// unreasonably far in the future.
func NormalizeTimestamp(ts time.Time, now time.Time) time.Time {
	if ts.IsZero() {
		return now.UTC()
	}
	if ts.After(now.Add(MaxFutureSkew)) {
		return now.UTC()
	}
	return ts.UTC()
}

// SniffMessage detects whether msg is a JSON object or logfmt line and, if so,
// returns the parsed key/value fields. ok is false for plain free-text.
func SniffMessage(msg string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return nil, false
	}

	// JSON object.
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var m map[string]any
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil && len(m) > 0 {
			return m, true
		}
	}

	// logfmt: at least one key=value pair and no spaces inside bare values.
	if fields, ok := parseLogfmt(trimmed); ok {
		return fields, true
	}

	return nil, false
}

// parseLogfmt parses simple logfmt (key=value, key="quoted value") lines. It is
// intentionally conservative: a line must contain at least one '=' and every
// space-delimited token (outside quotes) must be a key=value pair, otherwise
// it's treated as free text.
func parseLogfmt(s string) (map[string]any, bool) {
	if !strings.Contains(s, "=") {
		return nil, false
	}

	fields := map[string]any{}
	i := 0
	n := len(s)
	for i < n {
		// Skip leading spaces.
		for i < n && s[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}

		// Read key up to '='.
		keyStart := i
		for i < n && s[i] != '=' && s[i] != ' ' {
			i++
		}
		if i >= n || s[i] != '=' {
			// A bare token with no '=' means this isn't clean logfmt.
			return nil, false
		}
		key := s[keyStart:i]
		i++ // consume '='

		if key == "" {
			return nil, false
		}

		// Read value: quoted or bare.
		var val string
		if i < n && s[i] == '"' {
			i++
			valStart := i
			for i < n && s[i] != '"' {
				if s[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			val = s[valStart:i]
			if i < n {
				i++ // consume closing quote
			}
		} else {
			valStart := i
			for i < n && s[i] != ' ' {
				i++
			}
			val = s[valStart:i]
		}

		fields[key] = coerceScalar(val)
	}

	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

// coerceScalar converts a logfmt value to bool/int/float when it cleanly parses,
// otherwise returns the string.
func coerceScalar(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}
