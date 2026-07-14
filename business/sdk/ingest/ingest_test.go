package ingest

import (
	"testing"
	"time"

	"github.com/jkarage/logingestor/business/domain/logbus"
)

func Test_SyslogSeverityToLevel(t *testing.T) {
	cases := map[int]logbus.Level{
		0: logbus.LevelError, 1: logbus.LevelError, 2: logbus.LevelError, 3: logbus.LevelError,
		4: logbus.LevelWarn,
		5: logbus.LevelInfo, 6: logbus.LevelInfo,
		7: logbus.LevelDebug,
	}
	for sev, want := range cases {
		if got := SyslogSeverityToLevel(sev); !got.Equal(want) {
			t.Errorf("syslog sev %d = %s, want %s", sev, got, want)
		}
	}
}

func Test_OTelSeverityToLevel(t *testing.T) {
	cases := map[int]logbus.Level{
		1: logbus.LevelDebug, 8: logbus.LevelDebug,
		9: logbus.LevelInfo, 12: logbus.LevelInfo,
		13: logbus.LevelWarn, 16: logbus.LevelWarn,
		17: logbus.LevelError, 24: logbus.LevelError,
		0: logbus.LevelInfo, // out of range
	}
	for num, want := range cases {
		if got := OTelSeverityToLevel(num); !got.Equal(want) {
			t.Errorf("otel sev %d = %s, want %s", num, got, want)
		}
	}
}

func Test_NormalizeTimestamp(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	if got := NormalizeTimestamp(time.Time{}, now); !got.Equal(now) {
		t.Errorf("zero ts should clamp to now, got %v", got)
	}

	future := now.Add(48 * time.Hour)
	if got := NormalizeTimestamp(future, now); !got.Equal(now) {
		t.Errorf("far-future ts should clamp to now, got %v", got)
	}

	past := now.Add(-72 * time.Hour)
	if got := NormalizeTimestamp(past, now); !got.Equal(past) {
		t.Errorf("reasonable backfill should be preserved, got %v", got)
	}
}

func Test_SniffMessage_JSON(t *testing.T) {
	fields, ok := SniffMessage(`{"user":"bob","attempt":3}`)
	if !ok {
		t.Fatal("expected JSON to be sniffed")
	}
	if fields["user"] != "bob" {
		t.Errorf("user = %v, want bob", fields["user"])
	}
}

func Test_SniffMessage_Logfmt(t *testing.T) {
	fields, ok := SniffMessage(`level=info msg="started up" code=200 ok=true`)
	if !ok {
		t.Fatal("expected logfmt to be sniffed")
	}
	if fields["msg"] != "started up" {
		t.Errorf("msg = %v, want 'started up'", fields["msg"])
	}
	if fields["code"] != int64(200) {
		t.Errorf("code = %v (%T), want int64 200", fields["code"], fields["code"])
	}
	if fields["ok"] != true {
		t.Errorf("ok = %v, want true", fields["ok"])
	}
}

func Test_SniffMessage_PlainText(t *testing.T) {
	if _, ok := SniffMessage("just a plain log line with no structure"); ok {
		t.Error("plain text should not be sniffed as structured")
	}
}

func Test_Redactor(t *testing.T) {
	r := NewRedactor()

	cases := []struct{ in, mustNotContain string }{
		{"contact admin@example.com now", "admin@example.com"},
		{"client 192.168.1.42 connected", "192.168.1.42"},
		{"key ls_src_live_deadbeef0123 leaked", "ls_src_live_deadbeef0123"},
		{"Authorization: Bearer abc123def456", "abc123def456"},
	}
	for _, c := range cases {
		got := r.RedactString(c.in)
		if got == c.in {
			t.Errorf("expected redaction of %q", c.in)
		}
		if contains(got, c.mustNotContain) {
			t.Errorf("redacted output %q still contains secret %q", got, c.mustNotContain)
		}
	}
}

func Test_Redactor_Map(t *testing.T) {
	r := NewRedactor()
	m := map[string]any{
		"email":  "user@corp.io",
		"count":  5,
		"nested": map[string]any{"ip": "10.0.0.1"},
	}
	r.RedactMap(m)

	if m["email"] == "user@corp.io" {
		t.Error("top-level email not redacted")
	}
	if m["count"] != 5 {
		t.Errorf("non-string scalar mutated: %v", m["count"])
	}
	if nested := m["nested"].(map[string]any); nested["ip"] == "10.0.0.1" {
		t.Error("nested ip not redacted")
	}
}

func Test_Normalize_EndToEnd(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	rec := &Record{
		Level:      logbus.LevelInfo,
		Message:    `{"event":"login","email":"a@b.com"}`,
		Timestamp:  time.Time{}, // -> now
		Attributes: map[string]any{},
	}

	Normalize(rec, now, NewRedactor())

	if rec.Timestamp != now {
		t.Errorf("timestamp not normalized to now: %v", rec.Timestamp)
	}
	if rec.Attributes["event"] != "login" {
		t.Errorf("structured field not lifted: %v", rec.Attributes)
	}
	// The email inside the lifted attribute should be redacted.
	if rec.Attributes["email"] == "a@b.com" {
		t.Error("email in lifted attributes not redacted")
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
