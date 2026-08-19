package ssobus

import (
	"errors"
	"testing"

	"github.com/jkarage/logingestor/business/types/role"
)

func Test_NewConfig_Validate(t *testing.T) {
	valid := NewConfig{
		Issuer:       "https://idp.example.com",
		ClientID:     "client",
		ClientSecret: "secret",
		DefaultRole:  role.User,
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	t.Run("issuer must be https", func(t *testing.T) {
		// http would let a network attacker impersonate the IdP for discovery,
		// the token exchange and JWKS.
		for _, iss := range []string{
			"http://idp.example.com",
			"idp.example.com",
			"",
			"https://idp.example.com?foo=bar",
			"https://idp.example.com#frag",
			"https://",
		} {
			c := valid
			c.Issuer = iss
			if err := c.Validate(); !errors.Is(err, ErrInvalidIssuer) {
				t.Errorf("Issuer %q err = %v, want ErrInvalidIssuer", iss, err)
			}
		}
	})

	t.Run("SUPER ADMIN is never grantable by an IdP", func(t *testing.T) {
		c := valid
		c.DefaultRole = role.Admin
		if err := c.Validate(); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("err = %v, want ErrInvalidRole", err)
		}
	})

	t.Run("zero role rejected", func(t *testing.T) {
		c := valid
		c.DefaultRole = role.Role{}
		if err := c.Validate(); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("err = %v, want ErrInvalidRole", err)
		}
	})

	t.Run("client credentials required", func(t *testing.T) {
		for _, mut := range []func(*NewConfig){
			func(c *NewConfig) { c.ClientID = "" },
			func(c *NewConfig) { c.ClientSecret = "" },
		} {
			c := valid
			mut(&c)
			if err := c.Validate(); err == nil {
				t.Error("expected missing client credentials to fail")
			}
		}
	})
}

func Test_Config_PermitsEmail(t *testing.T) {
	t.Run("empty list permits all", func(t *testing.T) {
		c := Config{}
		if !c.PermitsEmail("anyone@anywhere.test") {
			t.Error("empty allow-list should permit everything")
		}
	})

	c := Config{AllowedDomains: []string{"example.com", "@corp.example.org"}}

	for _, ok := range []string{
		"person@example.com",
		"Person@EXAMPLE.COM",
		"someone@corp.example.org",
	} {
		if !c.PermitsEmail(ok) {
			t.Errorf("PermitsEmail(%q) = false, want true", ok)
		}
	}

	for _, bad := range []string{
		"person@evil.com",
		// Must not match on suffix: a lookalike domain is a different domain.
		"person@notexample.com",
		"person@example.com.evil.com",
		"no-at-sign",
		"",
	} {
		if c.PermitsEmail(bad) {
			t.Errorf("PermitsEmail(%q) = true, want false", bad)
		}
	}
}
