package ssoapp

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func Test_stateStore_RoundTripAndPKCE(t *testing.T) {
	s := newStateStore()
	org := uuid.New()
	now := time.Now()

	state, nonce, challenge, err := s.begin(org, now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	for name, v := range map[string]string{"state": state, "nonce": nonce, "challenge": challenge} {
		if len(v) < 32 {
			t.Errorf("%s is too short to be unguessable: %q", name, v)
		}
	}
	if state == nonce {
		t.Error("state and nonce must be independent values")
	}

	ls, err := s.consume(state, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if ls.orgID != org {
		t.Errorf("orgID = %s, want %s", ls.orgID, org)
	}
	if ls.nonce != nonce {
		t.Error("stored nonce must match the one sent to the IdP")
	}

	// The challenge must be S256 of the retained verifier, or the provider will
	// reject the exchange.
	sum := sha256.Sum256([]byte(ls.codeVerifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); want != challenge {
		t.Errorf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}
}

func Test_stateStore_Rejections(t *testing.T) {
	now := time.Now()

	t.Run("single use", func(t *testing.T) {
		s := newStateStore()
		state, _, _, _ := s.begin(uuid.New(), now)

		if _, err := s.consume(state, now); err != nil {
			t.Fatalf("first consume: %v", err)
		}
		if _, err := s.consume(state, now); !errors.Is(err, errStateInvalid) {
			t.Errorf("replay err = %v, want errStateInvalid", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		s := newStateStore()
		state, _, _, _ := s.begin(uuid.New(), now)

		if _, err := s.consume(state, now.Add(loginStateTTL+time.Second)); !errors.Is(err, errStateInvalid) {
			t.Errorf("err = %v, want errStateInvalid", err)
		}
	})

	t.Run("unknown and empty", func(t *testing.T) {
		s := newStateStore()
		for _, raw := range []string{"", "forged-state"} {
			if _, err := s.consume(raw, now); !errors.Is(err, errStateInvalid) {
				t.Errorf("consume(%q) err = %v, want errStateInvalid", raw, err)
			}
		}
	})

	t.Run("expired entries are swept", func(t *testing.T) {
		s := newStateStore()
		for range 4 {
			s.begin(uuid.New(), now)
		}
		s.begin(uuid.New(), now.Add(loginStateTTL+time.Second))

		if len(s.m) != 1 {
			t.Errorf("len = %d after sweep, want 1", len(s.m))
		}
	})
}

func Test_sessionStore(t *testing.T) {
	now := time.Now()

	t.Run("round trip is single use", func(t *testing.T) {
		s := newSessionStore()
		code, err := s.put("jwt-token", now)
		if err != nil {
			t.Fatalf("put: %v", err)
		}

		got, err := s.take(code, now)
		if err != nil {
			t.Fatalf("take: %v", err)
		}
		if got != "jwt-token" {
			t.Errorf("token = %q", got)
		}
		if _, err := s.take(code, now); !errors.Is(err, errSessionInvalid) {
			t.Errorf("replay err = %v, want errSessionInvalid", err)
		}
	})

	t.Run("expires", func(t *testing.T) {
		s := newSessionStore()
		code, _ := s.put("jwt-token", now)

		if _, err := s.take(code, now.Add(sessionCodeTTL+time.Second)); !errors.Is(err, errSessionInvalid) {
			t.Errorf("err = %v, want errSessionInvalid", err)
		}
	})

	// The hand-off code is short-lived by design; if this grows, a leaked
	// redirect URL becomes a usable session.
	t.Run("ttl stays short", func(t *testing.T) {
		if sessionCodeTTL > 5*time.Minute {
			t.Errorf("sessionCodeTTL = %v, too long for a URL-borne code", sessionCodeTTL)
		}
	})
}

func Test_randomPassword(t *testing.T) {
	seen := make(map[string]bool)

	for range 50 {
		pw, err := randomPassword()
		if err != nil {
			t.Fatalf("randomPassword: %v", err)
		}
		// Must satisfy the password type's own rules, or user creation fails.
		if pw.String() == "" {
			t.Fatal("empty password")
		}
		if seen[pw.String()] {
			t.Fatal("randomPassword returned a duplicate")
		}
		seen[pw.String()] = true
	}
}
