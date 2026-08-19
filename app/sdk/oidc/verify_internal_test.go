package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

const (
	testIssuer = "https://idp.example.com"
	testClient = "client-abc"
	testKID    = "kid-1"
	testNonce  = "nonce-xyz"
)

// harness builds a client pre-seeded with a signing key so no HTTP is needed.
func harness(t *testing.T) (*Client, *Provider, *rsa.PrivateKey) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p := &Provider{
		Issuer:        testIssuer,
		keys:          map[string]*rsa.PublicKey{testKID: &key.PublicKey},
		keysFetchedAt: time.Now(),
	}

	return New(time.Hour), p, key
}

// sign mints an ID token, letting each test bend one field.
func sign(t *testing.T, key *rsa.PrivateKey, alg jwt.SigningMethod, kid string, claims jwt.Claims) string {
	t.Helper()

	tok := jwt.NewWithClaims(alg, claims)
	tok.Header["kid"] = kid

	var (
		s   string
		err error
	)
	switch alg.(type) {
	case *jwt.SigningMethodRSA:
		s, err = tok.SignedString(key)
	default:
		s, err = tok.SignedString([]byte("hmac-secret"))
	}
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func goodClaims() *IDTokenClaims {
	return &IDTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "idp-user-1",
			Audience:  jwt.ClaimStrings{testClient},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Nonce:         testNonce,
		Email:         "Person@Example.com",
		EmailVerified: true,
		Name:          "A Person",
	}
}

func Test_VerifyIDToken_Success(t *testing.T) {
	c, p, key := harness(t)

	raw := sign(t, key, jwt.SigningMethodRS256, testKID, goodClaims())

	id, err := c.VerifyIDToken(context.Background(), p, raw, testClient, testNonce, true)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}

	if id.Subject != "idp-user-1" {
		t.Errorf("Subject = %q", id.Subject)
	}
	// Email must be normalised, or the same person could be provisioned twice
	// under different casings.
	if id.Email != "person@example.com" {
		t.Errorf("Email = %q, want lowercased", id.Email)
	}
}

func Test_VerifyIDToken_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*IDTokenClaims)
		alg     jwt.SigningMethod
		kid     string
		nonce   string
		wantErr error
	}{
		{
			name:    "wrong issuer",
			mutate:  func(c *IDTokenClaims) { c.Issuer = "https://evil.example.com" },
			wantErr: ErrVerify,
		},
		{
			// A token minted by the same IdP for a different client must not be
			// accepted here.
			name:    "wrong audience",
			mutate:  func(c *IDTokenClaims) { c.Audience = jwt.ClaimStrings{"some-other-client"} },
			wantErr: ErrVerify,
		},
		{
			name:    "expired",
			mutate:  func(c *IDTokenClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) },
			wantErr: ErrVerify,
		},
		{
			// Replaying a token from a different login attempt.
			name:    "nonce mismatch",
			nonce:   "a-different-nonce",
			wantErr: ErrVerify,
		},
		{
			name:    "empty expected nonce is never trusted",
			nonce:   "",
			wantErr: ErrVerify,
		},
		{
			name:    "unknown signing key",
			kid:     "kid-does-not-exist",
			wantErr: ErrVerify,
		},
		{
			// Algorithm confusion: an HMAC token must not be accepted, whatever
			// the key material.
			name:    "HMAC signed",
			alg:     jwt.SigningMethodHS256,
			wantErr: ErrVerify,
		},
		{
			name:    "no subject",
			mutate:  func(c *IDTokenClaims) { c.Subject = "" },
			wantErr: ErrVerify,
		},
		{
			name:    "no email",
			mutate:  func(c *IDTokenClaims) { c.Email = "" },
			wantErr: ErrNoEmail,
		},
		{
			// Unverified email is an account-takeover path onto an existing user.
			name:    "unverified email",
			mutate:  func(c *IDTokenClaims) { c.EmailVerified = false },
			wantErr: ErrEmailUnverified,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, p, key := harness(t)

			claims := goodClaims()
			if tc.mutate != nil {
				tc.mutate(claims)
			}

			alg := jwt.SigningMethod(jwt.SigningMethodRS256)
			if tc.alg != nil {
				alg = tc.alg
			}
			kid := testKID
			if tc.kid != "" {
				kid = tc.kid
			}
			nonce := testNonce
			if tc.nonce != "" || tc.name == "empty expected nonce is never trusted" {
				nonce = tc.nonce
			}

			raw := sign(t, key, alg, kid, claims)

			// A bad kid would otherwise trigger a JWKS refetch over the network.
			if tc.kid != "" {
				p.JWKSURL = "http://127.0.0.1:1/jwks"
			}

			_, err := c.VerifyIDToken(context.Background(), p, raw, testClient, nonce, true)
			if err == nil {
				t.Fatal("expected verification to fail")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// email_verified arrives as a bool from some providers and a string from others.
func Test_isTrue(t *testing.T) {
	for _, v := range []any{true, "true", "TRUE"} {
		if !isTrue(v) {
			t.Errorf("isTrue(%#v) = false, want true", v)
		}
	}
	for _, v := range []any{false, "false", "", nil, 1, "yes"} {
		if isTrue(v) {
			t.Errorf("isTrue(%#v) = true, want false", v)
		}
	}
}

func Test_rsaPublicKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	got, err := rsaPublicKey(n, e)
	if err != nil {
		t.Fatalf("rsaPublicKey: %v", err)
	}
	if got.N.Cmp(key.N) != 0 || got.E != key.E {
		t.Error("round-tripped key does not match the original")
	}

	if _, err := rsaPublicKey("", ""); err == nil {
		t.Error("empty modulus/exponent should fail")
	}
	if _, err := rsaPublicKey("!!!not-base64!!!", e); err == nil {
		t.Error("invalid base64 modulus should fail")
	}
}
