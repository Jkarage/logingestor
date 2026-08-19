package mid

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/foundation/web"
)

// setSource stores the authenticated source in the context.
func setSource(ctx context.Context, src sourcebus.Source) context.Context {
	return context.WithValue(ctx, sourceKey, src)
}

// GetSource returns the authenticated source from the context. It is only
// populated on routes protected by AuthenticateSource.
func GetSource(ctx context.Context) (sourcebus.Source, error) {
	v, ok := ctx.Value(sourceKey).(sourcebus.Source)
	if !ok {
		return sourcebus.Source{}, errors.New("source not found in context")
	}
	return v, nil
}

// AuthenticateSource validates a source-scoped ingest key on the Authorization
// header ("Bearer ls_src_live_…"). On success the resolved source is placed in
// the context (carrying its org_id/project_id and ingest controls). A missing
// or unrecognized key is 401; a recognized but disabled source is 403.
//
// The source binds the request to its own org/project — ingestion handlers must
// derive the target project from the source, never from client input, so a key
// can only ever write to its own tenant.
func AuthenticateSource(sourceBus sourcebus.ExtBusiness, projectBus projectbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			raw, err := bearerToken(r.Header.Get("authorization"))
			if err != nil {
				return errs.New(errs.Unauthenticated, err)
			}

			if !sourcebus.HasKeyScheme(raw) {
				return errs.New(errs.Unauthenticated, errors.New("invalid ingest key"))
			}

			src, err := sourceBus.QueryByKeyHash(ctx, sourcebus.HashKey(raw))
			if err != nil {
				// Unknown key — do not leak whether the hash matched anything.
				return errs.New(errs.Unauthenticated, errors.New("invalid ingest key"))
			}

			// Constant-time confirmation that the stored hash matches what the
			// presented key hashes to (defense in depth around the lookup).
			if subtle.ConstantTimeCompare([]byte(src.KeyHash), []byte(sourcebus.HashKey(raw))) != 1 {
				return errs.New(errs.Unauthenticated, errors.New("invalid ingest key"))
			}

			if !src.IsActive {
				return errs.New(errs.PermissionDenied, errors.New("source is disabled"))
			}

			// An expired key is refused with a distinct code so a shipper can tell
			// "rotate me" from "you were revoked".
			if src.Expired(time.Now()) {
				return errs.New(errs.KeyExpired, errors.New("ingest key has expired; rotate it to continue"))
			}

			// A suspended organization accepts no new logs. Alerts are driven by
			// ingestion, so stopping here stops those too.
			enabled, err := projectBus.OrgEnabled(ctx, src.ProjectID)
			if err != nil {
				return errs.Errorf(errs.Internal, "orgenabled: projectID[%s]: %s", src.ProjectID, err)
			}
			if !enabled {
				return errs.New(errs.OrgSuspended, errors.New("organization is suspended"))
			}

			ctx = setSource(ctx, src)
			return next(ctx, r)
		}
		return h
	}
	return m
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(header string) (string, error) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return "", errors.New("expected authorization header format: Bearer <key>")
	}
	return strings.TrimSpace(parts[1]), nil
}
