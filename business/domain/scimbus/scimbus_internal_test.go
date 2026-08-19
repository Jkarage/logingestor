package scimbus

import (
	"strings"
	"testing"
)

func Test_GenerateToken(t *testing.T) {
	seen := make(map[string]bool)

	for range 30 {
		raw, hash, prefix, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}

		if !HasKeyScheme(raw) {
			t.Errorf("raw %q lacks the scheme prefix", raw)
		}
		if len(raw) < 40 {
			t.Errorf("raw token too short to be unguessable: %d chars", len(raw))
		}
		if seen[raw] {
			t.Fatal("GenerateToken returned a duplicate")
		}
		seen[raw] = true

		// The stored hash must not be the token, or a database dump is a
		// usable provisioning credential.
		if hash == raw || strings.Contains(hash, raw) {
			t.Error("hash must not contain the raw token")
		}
		if hash != HashToken(raw) {
			t.Error("hash is not reproducible from the raw token")
		}

		// The prefix is for display only and must not be enough to authenticate.
		if !strings.HasPrefix(raw, prefix) {
			t.Errorf("prefix %q is not a prefix of the token", prefix)
		}
		if len(prefix) >= len(raw) {
			t.Error("prefix must be shorter than the token")
		}
	}
}

func Test_HasKeyScheme(t *testing.T) {
	raw, _, _, _ := GenerateToken()
	if !HasKeyScheme(raw) {
		t.Error("issued token should match the scheme")
	}
	for _, bad := range []string{"", "bearer-something", "ls_src_live_abc", "LS_SCIM_x"} {
		if HasKeyScheme(bad) {
			t.Errorf("HasKeyScheme(%q) = true, want false", bad)
		}
	}
}

func Test_HashToken_Deterministic(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Error("hashing must be deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Error("different tokens must hash differently")
	}
	if len(HashToken("abc")) != 64 {
		t.Errorf("expected a 64-char hex sha256, got %d", len(HashToken("abc")))
	}
}
