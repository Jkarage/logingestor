package mid

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// touchInterval throttles the last_used_at write. Recording the minute a key was
// last seen is useful; recording it on every request is a write per read.
const touchInterval = time.Minute

// setAPIKey stores the authenticated API key in the context.
func setAPIKey(ctx context.Context, k apikeybus.APIKey) context.Context {
	return context.WithValue(ctx, apiKeyKey, k)
}

// GetAPIKey returns the authenticated API key from the context. It is only
// populated on routes protected by AuthenticateAPIKey.
func GetAPIKey(ctx context.Context) (apikeybus.APIKey, error) {
	v, ok := ctx.Value(apiKeyKey).(apikeybus.APIKey)
	if !ok {
		return apikeybus.APIKey{}, errors.New("api key not found in context")
	}

	return v, nil
}

// AuthenticateAPIKey validates a read-only API key on the Authorization header
// ("Bearer ls_api_live_…") and places it in the context.
//
// It mirrors AuthenticateSource deliberately: the same header, the same
// constant-time confirmation, the same distinct code for an expired key so a
// script is told to rotate rather than that its key is wrong, and the same
// refusal for a suspended organization. What differs is the direction — this
// key only ever reads, and the query handlers derive their scope from it rather
// than from client input, so a key cannot reach another tenant's logs.
func AuthenticateAPIKey(keyBus *apikeybus.Business, orgBus orgbus.ExtBusiness) web.MidFunc {
	var (
		mu        sync.Mutex
		lastTouch = make(map[uuid.UUID]time.Time)
	)

	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			raw, err := bearerToken(r.Header.Get("authorization"))
			if err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			if !apikeybus.HasKeyScheme(raw) {
				return errs.New(errs.Unauthenticated, errors.New("invalid api key"))
			}

			key, err := keyBus.Authenticate(ctx, raw)
			switch {
			case errors.Is(err, apikeybus.ErrKeyExpired):
				return errs.New(errs.KeyExpired, errors.New("api key has expired; issue a new one to continue"))
			case err != nil:
				return errs.New(errs.Unauthenticated, errors.New("invalid api key"))
			}

			// Defense in depth around the hash lookup.
			if subtle.ConstantTimeCompare([]byte(key.KeyHash), []byte(apikeybus.HashKey(raw))) != 1 {
				return errs.New(errs.Unauthenticated, errors.New("invalid api key"))
			}

			// A suspended organization serves no reads either: suspension is a
			// lifecycle state, and a key is not a way around it. The key names its
			// org directly, so this is one lookup regardless of project scope.
			org, err := orgBus.QueryByID(ctx, key.OrgID)
			if err != nil {
				return errs.Errorf(errs.Internal, "querybyid: orgID[%s]: %s", key.OrgID, err)
			}
			if !org.Enabled {
				return errs.New(errs.OrgSuspended, errors.New("organization is suspended"))
			}

			now := time.Now()

			mu.Lock()
			last, seen := lastTouch[key.ID]
			due := !seen || now.Sub(last) >= touchInterval
			if due {
				lastTouch[key.ID] = now
			}
			mu.Unlock()

			if due {
				keyBus.TouchLastUsed(ctx, key.ID, now)
			}

			ctx = setAPIKey(ctx, key)

			return next(ctx, r)
		}

		return h
	}

	return m
}
