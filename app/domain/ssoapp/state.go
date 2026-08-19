package ssoapp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Lifetimes for the two short-lived artefacts in the login flow.
const (
	// loginStateTTL bounds how long a browser may take to come back from the IdP.
	loginStateTTL = 10 * time.Minute

	// sessionCodeTTL bounds the hand-off of a minted session token to the SPA.
	sessionCodeTTL = 60 * time.Second
)

var (
	errStateInvalid   = errors.New("sso state is invalid, expired, or already used")
	errSessionInvalid = errors.New("sso code is invalid, expired, or already used")
)

// loginState is the server-side half of one authorization request. Keeping the
// nonce and PKCE verifier here — never in a cookie or URL — means a stolen
// redirect cannot be replayed.
type loginState struct {
	orgID        uuid.UUID
	nonce        string
	codeVerifier string
	expiresAt    time.Time
}

// stateStore holds in-flight login states. In-memory is correct for a
// single-instance deployment; scaling out requires shared storage, since the
// callback must land on the process that started the request.
type stateStore struct {
	mu sync.Mutex
	m  map[string]loginState
}

func newStateStore() *stateStore {
	return &stateStore{m: make(map[string]loginState)}
}

// begin mints a state, nonce and PKCE pair for one login attempt.
func (s *stateStore) begin(orgID uuid.UUID, now time.Time) (state, nonce, challenge string, err error) {
	for _, dst := range []*string{&state, &nonce} {
		v, err := randomToken()
		if err != nil {
			return "", "", "", err
		}
		*dst = v
	}

	verifier, err := randomToken()
	if err != nil {
		return "", "", "", err
	}

	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range s.m {
		if now.After(v.expiresAt) {
			delete(s.m, k)
		}
	}

	s.m[state] = loginState{
		orgID:        orgID,
		nonce:        nonce,
		codeVerifier: verifier,
		expiresAt:    now.Add(loginStateTTL),
	}

	return state, nonce, challenge, nil
}

// consume redeems a state exactly once.
func (s *stateStore) consume(state string, now time.Time) (loginState, error) {
	if state == "" {
		return loginState{}, errStateInvalid
	}

	s.mu.Lock()
	ls, ok := s.m[state]
	delete(s.m, state)
	s.mu.Unlock()

	if !ok || now.After(ls.expiresAt) {
		return loginState{}, errStateInvalid
	}

	return ls, nil
}

// sessionStore holds minted session tokens awaiting collection by the SPA. The
// token is handed over through a POST body rather than a redirect URL, so it
// never reaches proxy or browser-history logs.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionEntry
}

type sessionEntry struct {
	token     string
	expiresAt time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: make(map[string]sessionEntry)}
}

func (s *sessionStore) put(token string, now time.Time) (string, error) {
	code, err := randomToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range s.m {
		if now.After(v.expiresAt) {
			delete(s.m, k)
		}
	}

	s.m[code] = sessionEntry{token: token, expiresAt: now.Add(sessionCodeTTL)}

	return code, nil
}

func (s *sessionStore) take(code string, now time.Time) (string, error) {
	if code == "" {
		return "", errSessionInvalid
	}

	s.mu.Lock()
	e, ok := s.m[code]
	delete(s.m, code)
	s.mu.Unlock()

	if !ok || now.After(e.expiresAt) {
		return "", errSessionInvalid
	}

	return e.token, nil
}

// randomToken returns 32 bytes of entropy as base64url text.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
