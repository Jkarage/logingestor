package ingest

import "regexp"

// Redactor masks secret-looking substrings before logs are persisted.
type Redactor struct {
	patterns []*regexp.Regexp
	mask     string
}

// Default secret patterns. Conservative but covers the common cases the spec
// calls out: emails, IPv4 addresses, credit-card-like digit runs, and bearer /
// long opaque tokens (incl. our own ls_*_live_ keys).
var defaultPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),                              // email
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),                                                   // IPv4
	regexp.MustCompile(`\b(?:\d[ -]*?){13,16}\b`),                                                       // credit-card-ish
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]+`),                                                    // bearer tokens
	regexp.MustCompile(`\bls_[a-z]+_live_[a-f0-9]+`),                                                    // our ingest keys
	regexp.MustCompile(`(?i)(?:api[_-]?key|secret|token|password)["']?\s*[:=]\s*["']?[a-z0-9._\-]{6,}`), // key=value secrets
}

// DefaultMask is the replacement string for redacted matches.
const DefaultMask = "[REDACTED]"

// NewRedactor builds a redactor from the default secret patterns.
func NewRedactor() *Redactor {
	return &Redactor{patterns: defaultPatterns, mask: DefaultMask}
}

// RedactString masks all secret patterns in s.
func (r *Redactor) RedactString(s string) string {
	if r == nil || s == "" {
		return s
	}
	for _, p := range r.patterns {
		s = p.ReplaceAllString(s, r.mask)
	}
	return s
}

// RedactMap walks m and redacts string values (recursing into nested maps and
// slices). Non-string scalars are left as-is.
func (r *Redactor) RedactMap(m map[string]any) {
	if r == nil {
		return
	}
	for k, v := range m {
		m[k] = r.redactValue(v)
	}
}

func (r *Redactor) redactValue(v any) any {
	switch t := v.(type) {
	case string:
		return r.RedactString(t)
	case map[string]any:
		r.RedactMap(t)
		return t
	case []any:
		for i := range t {
			t[i] = r.redactValue(t[i])
		}
		return t
	default:
		return v
	}
}
