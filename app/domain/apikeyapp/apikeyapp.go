// Package apikeyapp maintains the app layer api for read-only API keys.
package apikeyapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// APIKey is a key as returned by the API. The secret is absent: it is shown
// exactly once, by the create response.
type APIKey struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ProjectID  *string `json:"projectId"`
	KeyPrefix  string  `json:"keyPrefix"`
	IsActive   bool    `json:"isActive"`
	CreatedBy  *string `json:"createdBy"`
	LastUsedAt *string `json:"lastUsedAt"`
	ExpiresAt  *string `json:"expiresAt"`

	// Expired is derived so a client does not have to compare clocks.
	Expired bool `json:"expired"`

	// RateLimitPerMin and RateLimitBurst are this key's query API budget. Zero
	// means it is on the service default, which the response also reports.
	RateLimitPerMin int `json:"rateLimitPerMin"`
	RateLimitBurst  int `json:"rateLimitBurst"`

	DateCreated string `json:"dateCreated"`
}

// Encode implements the encoder interface.
func (app APIKey) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// APIKeys is the list response shape.
type APIKeys struct {
	APIKeys []APIKey `json:"apiKeys"`

	// DefaultRatePerMin and DefaultRateBurst are what a key with a zero limit
	// gets, so a client can render "default (120/min)" rather than "0".
	DefaultRatePerMin int `json:"defaultRatePerMin"`
	DefaultRateBurst  int `json:"defaultRateBurst"`
}

// Encode implements the encoder interface.
func (app APIKeys) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// APIKeyCreated carries the raw key, returned exactly once.
type APIKeyCreated struct {
	APIKey
	Key string `json:"key"`
}

// Encode implements the encoder interface.
func (app APIKeyCreated) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// HTTPStatus returns 201 for a minted key.
func (app APIKeyCreated) HTTPStatus() int { return http.StatusCreated }

// NewAPIKey is the request body for minting a key.
type NewAPIKey struct {
	Name string `json:"name"`

	// ProjectID pins the key to one project. Omitted means every project in the
	// org, now and later.
	ProjectID string `json:"projectId"`

	// ExpiresInDays bounds the key's life. Omitted or 0 never expires.
	ExpiresInDays int `json:"expiresInDays"`

	// RateLimitPerMin and RateLimitBurst override the default query API budget.
	// Omitted or 0 leaves the key on the default.
	RateLimitPerMin int `json:"rateLimitPerMin"`
	RateLimitBurst  int `json:"rateLimitBurst"`
}

// Decode implements the decoder interface.
func (app *NewAPIKey) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toAppAPIKey(k apikeybus.APIKey, now time.Time) APIKey {
	out := APIKey{
		ID:              k.ID.String(),
		Name:            k.Name,
		KeyPrefix:       k.KeyPrefix,
		IsActive:        k.IsActive,
		Expired:         k.Expired(now),
		RateLimitPerMin: k.RateLimitPerMin,
		RateLimitBurst:  k.RateLimitBurst,
		DateCreated:     k.DateCreated.Format(time.RFC3339),
	}

	if k.ProjectID != nil {
		s := k.ProjectID.String()
		out.ProjectID = &s
	}
	if k.CreatedBy != nil {
		s := k.CreatedBy.String()
		out.CreatedBy = &s
	}
	if k.LastUsedAt != nil {
		s := k.LastUsedAt.Format(time.RFC3339)
		out.LastUsedAt = &s
	}
	if k.ExpiresAt != nil {
		s := k.ExpiresAt.Format(time.RFC3339)
		out.ExpiresAt = &s
	}

	return out
}

type app struct {
	keyBus     *apikeybus.Business
	projectBus projectbus.ExtBusiness

	// The service default, reported alongside the keys so a zero on a key is
	// readable rather than mysterious.
	defaultRatePerMin int
	defaultRateBurst  int
}

func newApp(cfg Config) *app {
	return &app{
		keyBus:            cfg.APIKeyBus,
		projectBus:        cfg.ProjectBus,
		defaultRatePerMin: cfg.DefaultRatePerMin,
		defaultRateBurst:  cfg.DefaultRateBurst,
	}
}

// query lists the org's keys.
// GET /v1/orgs/{org_id}/api-keys
func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	keys, err := a.keyBus.QueryByOrg(ctx, orgID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
	}

	now := time.Now()

	out := make([]APIKey, len(keys))
	for i, k := range keys {
		out[i] = toAppAPIKey(k, now)
	}

	return APIKeys{APIKeys: out, DefaultRatePerMin: a.defaultRatePerMin, DefaultRateBurst: a.defaultRateBurst}
}

// create mints a key.
// POST /v1/orgs/{org_id}/api-keys
func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	var body NewAPIKey
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	nk := apikeybus.NewAPIKey{
		Name:            body.Name,
		RateLimitPerMin: body.RateLimitPerMin,
		RateLimitBurst:  body.RateLimitBurst,
	}

	if body.ProjectID != "" {
		projectID, err := uuid.Parse(body.ProjectID)
		if err != nil {
			return errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
		}

		// The project must belong to this org, or a key would be a way to read
		// across tenants.
		project, err := a.projectBus.QueryByID(ctx, projectID)
		if err != nil {
			if errors.Is(err, projectbus.ErrNotFound) {
				return errs.New(errs.NotFound, errors.New("project not found in org"))
			}
			return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
		}
		if project.OrgID != orgID {
			return errs.New(errs.NotFound, errors.New("project not found in org"))
		}

		nk.ProjectID = &projectID
	}

	if body.ExpiresInDays < 0 {
		return errs.New(errs.InvalidArgument, errors.New("expiresInDays must be zero (never expires) or positive"))
	}
	if body.ExpiresInDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, body.ExpiresInDays)
		nk.ExpiresAt = &t
	}

	key, raw, err := a.keyBus.Create(ctx, orgID, mid.GetSubjectID(ctx), nk)
	if err != nil {
		switch {
		case errors.Is(err, apikeybus.ErrNameRequired),
			errors.Is(err, apikeybus.ErrNameTooLong),
			errors.Is(err, apikeybus.ErrExpiryPast):
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "create: orgID[%s]: %s", orgID, err)
	}

	return APIKeyCreated{APIKey: toAppAPIKey(key, time.Now()), Key: raw}
}

// revoke deletes a key.
// DELETE /v1/orgs/{org_id}/api-keys/{key_id}
func (a *app) revoke(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	keyID, err := uuid.Parse(web.Param(r, "key_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	key, err := a.keyBus.QueryByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, apikeybus.ErrNotFound) {
			return errs.New(errs.NotFound, apikeybus.ErrNotFound)
		}
		return errs.Errorf(errs.Internal, "querybyid: keyID[%s]: %s", keyID, err)
	}

	// A key from another org reads as missing, not as forbidden.
	if key.OrgID != orgID {
		return errs.New(errs.NotFound, apikeybus.ErrNotFound)
	}

	if err := a.keyBus.Revoke(ctx, key.ID); err != nil {
		return errs.Errorf(errs.Internal, "revoke: keyID[%s]: %s", key.ID, err)
	}

	return nil
}
