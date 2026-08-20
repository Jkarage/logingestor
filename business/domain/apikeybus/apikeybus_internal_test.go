package apikeybus

import (
	"strings"
	"testing"
	"time"

	"github.com/jkarage/logingestor/business/domain/sourcebus"
)

// The two key schemes must stay distinguishable: the prefix is what lets a
// request be rejected as "wrong kind of key" before any database lookup, and it
// is what keeps a read key from ever reaching the ingest path.
func Test_KeySchemesDoNotOverlap(t *testing.T) {
	raw, hash, prefix, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if !HasKeyScheme(raw) {
		t.Errorf("a generated key %q is not recognised by its own scheme", raw)
	}
	if sourcebus.HasKeyScheme(raw) {
		t.Errorf("an API key is accepted as an ingest key")
	}

	ingest, _, _, err := sourcebus.GenerateKey()
	if err != nil {
		t.Fatalf("sourcebus.GenerateKey: %v", err)
	}
	if HasKeyScheme(ingest) {
		t.Errorf("an ingest key is accepted as an API key")
	}

	if !strings.HasPrefix(prefix, KeyScheme) || len(prefix) >= len(raw) {
		t.Errorf("prefix %q is not a short display form of the key", prefix)
	}
	if strings.Contains(prefix, strings.TrimPrefix(raw, prefix)) {
		t.Errorf("prefix %q leaks the secret portion", prefix)
	}
	if hash != HashKey(raw) || hash == raw {
		t.Errorf("the stored hash is not a hash of the key")
	}
	if len(raw) != len(KeyScheme)+64 {
		t.Errorf("key is %d characters, want the scheme plus 64 hex", len(raw))
	}
}

func Test_APIKey_Expired(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	if (APIKey{}).Expired(now) {
		t.Errorf("a key with no expiry reports as expired")
	}
	if !(APIKey{ExpiresAt: &past}).Expired(now) {
		t.Errorf("a lapsed key does not report as expired")
	}
	if (APIKey{ExpiresAt: &future}).Expired(now) {
		t.Errorf("a key expiring later reports as expired")
	}
}
