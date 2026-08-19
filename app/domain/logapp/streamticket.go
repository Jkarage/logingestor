package logapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/foundation/web"
)

// streamTicketTTL bounds how long a ticket stays redeemable. It only has to
// cover the gap between the client fetching a ticket and opening the WebSocket,
// so it is deliberately short.
const streamTicketTTL = 30 * time.Second

// Errors returned by ticketStore.consume, mapped to handshake statuses by the
// stream handler: unknown/expired is 401, wrong project is 403.
var (
	errTicketInvalid = errors.New("stream ticket is invalid or expired")
	errTicketProject = errors.New("stream ticket was issued for a different project")
)

// ticketEntry is a single outstanding ticket.
type ticketEntry struct {
	userID    uuid.UUID
	projectID uuid.UUID
	expiresAt time.Time
}

// ticketStore holds single-use, short-TTL WebSocket tickets in memory.
//
// In-memory is correct only while the API runs as a single instance: a ticket
// issued by one process would not be redeemable on another. If the deployment
// is ever scaled out, this must move to shared storage (a Postgres table keyed
// by ticket hash, or Redis) — a stateless signed ticket would also work but
// could be replayed within its TTL, losing the single-use property.
type ticketStore struct {
	mu sync.Mutex
	m  map[string]ticketEntry
}

func newTicketStore() *ticketStore {
	return &ticketStore{m: make(map[string]ticketEntry)}
}

// issue mints a ticket bound to one user and one project.
func (s *ticketStore) issue(userID, projectID uuid.UUID, now time.Time) (string, time.Duration, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, fmt.Errorf("read random: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Opportunistic sweep; outstanding tickets are few and short-lived, so this
	// keeps the map bounded without a background goroutine.
	for k, v := range s.m {
		if now.After(v.expiresAt) {
			delete(s.m, k)
		}
	}

	s.m[raw] = ticketEntry{userID: userID, projectID: projectID, expiresAt: now.Add(streamTicketTTL)}

	return raw, streamTicketTTL, nil
}

// consume redeems a ticket for projectID and returns the user it was issued to.
// The ticket is removed whether or not it matches, so it can never be reused.
func (s *ticketStore) consume(raw string, projectID uuid.UUID, now time.Time) (uuid.UUID, error) {
	if raw == "" {
		return uuid.UUID{}, errTicketInvalid
	}

	s.mu.Lock()
	entry, ok := s.m[raw]
	delete(s.m, raw)
	s.mu.Unlock()

	if !ok || now.After(entry.expiresAt) {
		return uuid.UUID{}, errTicketInvalid
	}

	if entry.projectID != projectID {
		return uuid.UUID{}, errTicketProject
	}

	return entry.userID, nil
}

// StreamTicketResponse is returned by POST /projects/{project_id}/logs/stream-ticket.
type StreamTicketResponse struct {
	Ticket           string `json:"ticket"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

// Encode implements the encoder interface.
func (r StreamTicketResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// streamTicket issues a single-use, short-TTL ticket the client passes to the
// WebSocket endpoint instead of its JWT, keeping the token out of the query
// string (and therefore out of proxy and load-balancer access logs).
//
// POST /v1/projects/{project_id}/logs/stream-ticket
//
// The route is already gated by Authenticate + AuthorizeProjectAccess, so
// reaching this handler is itself proof the caller may read this project.
func (a *app) streamTicket(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	userID, err := mid.GetUserID(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	ticket, ttl, err := a.tickets.issue(userID, projectID, time.Now())
	if err != nil {
		return errs.Errorf(errs.Internal, "issue stream ticket: %s", err)
	}

	return StreamTicketResponse{
		Ticket:           ticket,
		ExpiresInSeconds: int(ttl.Seconds()),
	}
}

// streamError writes the standard error envelope before the WebSocket upgrade,
// so the client can tell an auth failure from an ordinary disconnect by reading
// the handshake status.
func streamError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	body := struct {
		Err     string `json:"error"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Err: code, Code: code, Message: message}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}
