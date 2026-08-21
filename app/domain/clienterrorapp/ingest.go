package clienterrorapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

// Abuse controls for the one endpoint that accepts anonymous writes.
//
// Everything here is deliberately cheap and in-process: the point is to shed a
// flood before it reaches the database, and a limiter that needs a network round
// trip to decide would be part of the problem.
const (
	// ipRatePerMinute is how many events one address may report per minute.
	// A page in a crash loop legitimately produces a burst, so the burst is
	// generous and the sustained rate is not.
	ipRatePerMinute = 120
	ipBurst         = 240

	// bucketSweepInterval is how often idle rate-limit buckets are dropped, so
	// the map cannot grow without bound on a long-lived process.
	bucketSweepInterval = 10 * time.Minute
)

// ingestLimiter is a token bucket per client address.
//
// It is keyed by address rather than by session because the abusive case has no
// session, and the address is never stored — it lives in this map and nowhere
// else, which keeps an IP out of the database entirely.
type ingestLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	swept   time.Time
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

func newIngestLimiter() *ingestLimiter {
	return &ingestLimiter{buckets: make(map[string]*ipBucket), swept: time.Now()}
}

// allow reports whether n events are permitted for key, and how long to wait if
// they are not.
func (l *ingestLimiter) allow(key string, n int, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.swept) > bucketSweepInterval {
		for k, b := range l.buckets {
			if now.Sub(b.last) > bucketSweepInterval {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &ipBucket{tokens: ipBurst, last: now}
		l.buckets[key] = b
	}

	// Refill from elapsed time.
	rate := float64(ipRatePerMinute) / 60.0
	b.tokens = min(float64(ipBurst), b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now

	if b.tokens < float64(n) {
		wait := time.Duration((float64(n)-b.tokens)/rate*float64(time.Second)) + time.Second
		return false, wait
	}

	b.tokens -= float64(n)

	return true, 0
}

// ingest handles POST /v1/client-errors.
//
// Authentication is optional on purpose: the errors that matter most happen on
// the landing and login pages, before there is a session to authenticate with.
// A token, when present, is trusted for identity and nothing else — the client's
// own idea of who it is never reaches storage.
func (a *app) ingest(ctx context.Context, r *http.Request) web.Encoder {
	if resp := a.checkOrigin(r); resp != nil {
		return resp
	}

	// Read with a hard ceiling before parsing. An unauthenticated endpoint that
	// buffers whatever it is sent is a memory exhaustion primitive.
	body, err := io.ReadAll(io.LimitReader(r.Body, clienterrorbus.MaxBodyBytes+1))
	if err != nil {
		return errs.New(errs.InvalidArgument, errors.New("could not read the request body"))
	}
	if len(body) > clienterrorbus.MaxBodyBytes {
		return errs.New(errs.PayloadTooLarge, errors.New("report exceeds the size limit"))
	}

	var req IngestRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return errs.New(errs.InvalidArgument, errors.New("body must be JSON with an events array"))
	}

	switch {
	case len(req.Events) == 0:
		return errs.New(errs.InvalidArgument, clienterrorbus.ErrNoEvents)
	case len(req.Events) > clienterrorbus.MaxBatchEvents:
		return errs.New(errs.InvalidArgument, clienterrorbus.ErrTooManyEvents)
	}

	// Rate limit by the number of events, not by request, or a client can batch
	// its way around the limit.
	if ok, retry := a.limiter.allow(clientKey(r), len(req.Events), time.Now()); !ok {
		if w := web.GetWriter(ctx); w != nil {
			w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
		}
		return errs.New(errs.TooManyRequests, errors.New("too many error reports; slow down"))
	}

	who := a.reporter(ctx, r, req)

	n, err := a.clientErrorBus.Ingest(ctx, who, toBusEvents(req))
	if err != nil {
		switch {
		case errors.Is(err, clienterrorbus.ErrNoEvents), errors.Is(err, clienterrorbus.ErrTooManyEvents):
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "ingest: %s", err)
	}

	return Accepted{Accepted: n}
}

// reporter resolves who is reporting, from the token rather than the payload.
//
// The client's org hint is only honoured if the authenticated user is actually a
// member of it, and the role always comes from that membership. Otherwise a
// report could be filed against another tenant's dashboard — which is both a
// data-quality problem and a way to probe which org ids exist.
func (a *app) reporter(ctx context.Context, r *http.Request, req IngestRequest) clienterrorbus.Reporter {
	var who clienterrorbus.Reporter

	header := r.Header.Get("authorization")
	if header == "" || a.authClient == nil {
		return who
	}

	resp, err := a.authClient.Authenticate(ctx, header)
	if err != nil {
		// An expired token on a crashing page is expected, and the report is
		// still worth keeping. Anonymous is the honest answer.
		return who
	}

	userID := resp.UserID
	who.UserID = &userID

	// A super admin may report against any org, matching the bypass everywhere
	// else; everyone else is checked against their memberships.
	superAdmin := false
	for _, r := range resp.Claims.Roles {
		if r == role.Admin.String() {
			superAdmin = true
			break
		}
	}

	hint := firstOrgHint(req)
	if hint == uuid.Nil {
		return who
	}

	if superAdmin {
		who.OrgID = &hint
		who.Role = role.Admin.String()

		return who
	}

	orgs, err := a.orgBus.QueryByUserID(ctx, userID)
	if err != nil {
		return who
	}

	for _, o := range orgs {
		if o.ID == hint {
			id := o.ID
			who.OrgID = &id
			who.Role = o.Role.String()
			break
		}
	}

	return who
}

// firstOrgHint returns the org id the batch claims to belong to. A batch comes
// from one page in one session, so the first usable hint speaks for all of it.
func firstOrgHint(req IngestRequest) uuid.UUID {
	for _, e := range req.Events {
		if e.OrgID == "" {
			continue
		}
		if id, err := uuid.Parse(e.OrgID); err == nil {
			return id
		}
	}

	return uuid.Nil
}

// checkOrigin rejects a browser report that did not come from one of our own
// pages. It is not a security boundary — anything can forge a header — but it
// keeps the noise of scanners and other sites' scripts out of the data.
func (a *app) checkOrigin(r *http.Request) web.Encoder {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// A beacon fired during unload, or a non-browser client. There is nothing
		// to check and refusing would drop the reports we most want.
		return nil
	}

	for _, allowed := range a.allowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return nil
		}
	}

	return errs.New(errs.PermissionDenied, errors.New("origin is not allowed to report errors"))
}

// clientKey identifies the caller for rate limiting. It prefers the proxy's
// forwarded address, since every request arrives through one.
func clientKey(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// The left-most entry is the original client; the rest are proxies.
		if i := strings.IndexByte(fwd, ','); i > 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}

	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}

	return r.RemoteAddr
}
