package sourcebus

import (
	"crypto/subtle"
	"strings"
	"testing"
)

func Test_GenerateKey(t *testing.T) {
	raw, hash, prefix, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if !strings.HasPrefix(raw, KeyScheme) {
		t.Errorf("raw key %q missing scheme %q", raw, KeyScheme)
	}
	// scheme (12) + 64 hex chars of 32 random bytes.
	if want := len(KeyScheme) + 64; len(raw) != want {
		t.Errorf("raw key length = %d, want %d", len(raw), want)
	}
	if !strings.HasPrefix(prefix, KeyScheme) {
		t.Errorf("prefix %q missing scheme", prefix)
	}
	if prefix == raw {
		t.Error("prefix must not equal the full key")
	}
	if !HasKeyScheme(raw) {
		t.Error("HasKeyScheme should accept a generated key")
	}

	// Hash must be the SHA-256 of the raw key and stable.
	if HashKey(raw) != hash {
		t.Error("returned hash does not match HashKey(raw)")
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(HashKey(raw))) != 1 {
		t.Error("hash not reproducible")
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(hash))
	}
}

func Test_GenerateKey_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for range 100 {
		raw, hash, _, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if _, dup := seen[hash]; dup {
			t.Fatalf("duplicate key hash generated: %q", raw)
		}
		seen[hash] = struct{}{}
	}
}

func Test_HasKeyScheme(t *testing.T) {
	if HasKeyScheme("ls_live_abc") {
		t.Error("non-source key scheme should be rejected")
	}
	if HasKeyScheme("") {
		t.Error("empty token should be rejected")
	}
}

func Test_ValidKind(t *testing.T) {
	for _, k := range []string{"otel", "syslog", "fluentbit", "vector", "k8s", "http"} {
		if !ValidKind(k) {
			t.Errorf("kind %q should be valid", k)
		}
	}
	if ValidKind("kafka") {
		t.Error("kind kafka should be invalid")
	}
}
