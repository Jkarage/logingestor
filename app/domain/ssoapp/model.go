// Package ssoapp maintains the app layer api for organization single sign-on.
package ssoapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/types/role"
)

// SSOConfig is the provider configuration as returned by the API. The client
// secret is deliberately absent: it is write-only.
type SSOConfig struct {
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"clientId"`
	DefaultRole    string   `json:"defaultRole"`
	AllowedDomains []string `json:"allowedDomains"`
	Enabled        bool     `json:"enabled"`
	HasSecret      bool     `json:"hasSecret"`
	DateCreated    string   `json:"dateCreated"`
	DateUpdated    string   `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app SSOConfig) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppSSOConfig(bus ssobus.Config) SSOConfig {
	domains := bus.AllowedDomains
	if domains == nil {
		domains = []string{}
	}

	return SSOConfig{
		Issuer:         bus.Issuer,
		ClientID:       bus.ClientID,
		DefaultRole:    bus.DefaultRole.String(),
		AllowedDomains: domains,
		Enabled:        bus.Enabled,
		HasSecret:      bus.ClientSecret != "",
		DateCreated:    bus.DateCreated.Format(time.RFC3339),
		DateUpdated:    bus.DateUpdated.Format(time.RFC3339),
	}
}

// UpsertSSOConfig is the request body for PUT /v1/orgs/{org_id}/sso.
type UpsertSSOConfig struct {
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"clientId"`
	ClientSecret   string   `json:"clientSecret"`
	DefaultRole    string   `json:"defaultRole"`
	AllowedDomains []string `json:"allowedDomains"`
	Enabled        *bool    `json:"enabled"`
}

// Decode implements the decoder interface.
func (app *UpsertSSOConfig) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toBusNewConfig(app UpsertSSOConfig) (ssobus.NewConfig, error) {
	var fieldErrors errs.FieldErrors

	if strings.TrimSpace(app.Issuer) == "" {
		fieldErrors.Add("issuer", fmt.Errorf("issuer is required"))
	}
	if strings.TrimSpace(app.ClientID) == "" {
		fieldErrors.Add("clientId", fmt.Errorf("clientId is required"))
	}
	if strings.TrimSpace(app.ClientSecret) == "" {
		fieldErrors.Add("clientSecret", fmt.Errorf("clientSecret is required"))
	}

	// Default to the least-privileged role so a first SSO login cannot hand out
	// administrative access by omission.
	defaultRole := role.User
	if app.DefaultRole != "" {
		r, err := role.Parse(app.DefaultRole)
		switch {
		case err != nil:
			fieldErrors.Add("defaultRole", fmt.Errorf("must be one of: ORG ADMIN, PROJECT MANAGER, VIEWER"))
		case r == role.Admin:
			fieldErrors.Add("defaultRole", fmt.Errorf("SUPER ADMIN cannot be granted by an identity provider"))
		default:
			defaultRole = r
		}
	}

	if len(fieldErrors) > 0 {
		return ssobus.NewConfig{}, fieldErrors.ToError()
	}

	enabled := true
	if app.Enabled != nil {
		enabled = *app.Enabled
	}

	domains := make([]string, 0, len(app.AllowedDomains))
	for _, d := range app.AllowedDomains {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, strings.ToLower(strings.TrimPrefix(d, "@")))
		}
	}

	return ssobus.NewConfig{
		Issuer:         app.Issuer,
		ClientID:       app.ClientID,
		ClientSecret:   app.ClientSecret,
		DefaultRole:    defaultRole,
		AllowedDomains: domains,
		Enabled:        enabled,
	}, nil
}

// ExchangeRequest is the body for POST /v1/auth/sso/exchange.
type ExchangeRequest struct {
	Code string `json:"code"`
}

// Decode implements the decoder interface.
func (app *ExchangeRequest) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// SessionResponse carries the minted session token to the SPA.
type SessionResponse struct {
	Token            string `json:"token"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

// Encode implements the encoder interface.
func (app SessionResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}
