package oidc

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v4"
)

// IDTokenClaims is the identity we take from a verified ID token.
type IDTokenClaims struct {
	jwt.RegisteredClaims

	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
}

// Identity is the verified subject of an ID token.
type Identity struct {
	Subject string
	Email   string
	Name    string
}

// VerifyIDToken checks the signature, issuer, audience, expiry and nonce of an
// ID token and returns the identity it asserts.
//
// requireVerifiedEmail rejects tokens whose email_verified claim is not true.
// That matters because the email is what binds an IdP subject to a local user:
// an IdP that lets a user self-assert an unverified address would otherwise be
// an account-takeover path onto an existing account.
func (c *Client) VerifyIDToken(ctx context.Context, p *Provider, rawToken, clientID, expectedNonce string, requireVerifiedEmail bool) (Identity, error) {
	var claims IDTokenClaims

	// WithValidMethods is the algorithm allow-list: it rejects "none" and any
	// HMAC-signed token before the keyfunc runs, which is what closes the
	// algorithm-confusion hole where an attacker signs with the public key.
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}))

	// Expiry, not-before and issued-at are validated by the parser. Issuer and
	// audience are checked below because this jwt version has no parser option
	// for them, and skipping them would accept a token minted for a different
	// tenant by the same provider.
	_, err := parser.ParseWithClaims(rawToken, &claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != "RS256" {
			return nil, ErrUnsupportedAlg
		}

		kid, _ := t.Header["kid"].(string)

		return c.keyFor(ctx, p, kid)
	})
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %s", ErrVerify, err)
	}

	if !claims.VerifyIssuer(p.Issuer, true) {
		return Identity{}, fmt.Errorf("%w: issuer %q is not %q", ErrVerify, claims.Issuer, p.Issuer)
	}

	if !claims.VerifyAudience(clientID, true) {
		return Identity{}, fmt.Errorf("%w: token audience does not include this client", ErrVerify)
	}

	// The nonce binds this token to the authorization request this server
	// started, so a token replayed from another login cannot be accepted.
	if expectedNonce == "" || claims.Nonce != expectedNonce {
		return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrVerify)
	}

	if claims.Subject == "" {
		return Identity{}, fmt.Errorf("%w: no subject", ErrVerify)
	}

	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		return Identity{}, ErrNoEmail
	}

	if requireVerifiedEmail && !isTrue(claims.EmailVerified) {
		return Identity{}, ErrEmailUnverified
	}

	return Identity{Subject: claims.Subject, Email: email, Name: claims.Name}, nil
}

// isTrue reads email_verified, which providers send as either a bool or the
// strings "true"/"false".
func isTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	}
	return false
}
