// Package oidc implements the slice of OpenID Connect needed for enterprise
// single sign-on: provider discovery, the authorization-code exchange, and
// ID-token verification against the provider's JWKS.
//
// Only RS256 is accepted. Every mainstream enterprise IdP (Okta, Entra ID,
// Google Workspace, Auth0) signs ID tokens with RS256 by default, and refusing
// the rest keeps the verification surface small — notably it makes algorithm
// confusion and the "alg":"none" family unrepresentable.
package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Errors returned by this package.
var (
	ErrDiscovery       = errors.New("oidc: provider discovery failed")
	ErrTokenExchange   = errors.New("oidc: authorization code exchange failed")
	ErrNoIDToken       = errors.New("oidc: provider returned no id_token")
	ErrVerify          = errors.New("oidc: id_token verification failed")
	ErrUnsupportedAlg  = errors.New("oidc: id_token must be signed with RS256")
	ErrNoEmail         = errors.New("oidc: id_token has no email claim")
	ErrEmailUnverified = errors.New("oidc: id_token email is not verified")
)

// httpTimeout bounds every outbound call to an IdP so a hung provider cannot
// pin a request goroutine.
const httpTimeout = 10 * time.Second

// Provider is a discovered OIDC provider.
type Provider struct {
	Issuer        string
	AuthURL       string
	TokenURL      string
	JWKSURL       string
	discoveredAt  time.Time
	keys          map[string]*rsa.PublicKey
	keysFetchedAt time.Time
	mu            sync.Mutex
}

// discoveryDoc is the subset of the well-known document we rely on.
type discoveryDoc struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURL  string `json:"jwks_uri"`
}

// Client discovers and caches providers by issuer.
type Client struct {
	http      *http.Client
	cacheTTL  time.Duration
	mu        sync.Mutex
	providers map[string]*Provider
}

// New constructs an OIDC client. Discovery documents and JWKS are cached for
// cacheTTL; pass zero for a one-hour default.
func New(cacheTTL time.Duration) *Client {
	if cacheTTL <= 0 {
		cacheTTL = time.Hour
	}

	return &Client{
		http:      &http.Client{Timeout: httpTimeout},
		cacheTTL:  cacheTTL,
		providers: make(map[string]*Provider),
	}
}

// Discover resolves an issuer to its endpoints, caching the result.
func (c *Client) Discover(ctx context.Context, issuer string) (*Provider, error) {
	issuer = strings.TrimSuffix(issuer, "/")

	c.mu.Lock()
	p, ok := c.providers[issuer]
	c.mu.Unlock()

	if ok && time.Since(p.discoveredAt) < c.cacheTTL {
		return p, nil
	}

	endpoint := issuer + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDiscovery, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDiscovery, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s returned %d", ErrDiscovery, endpoint, resp.StatusCode)
	}

	var doc discoveryDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: decode: %s", ErrDiscovery, err)
	}

	// The issuer in the document must match what we asked for, or a misconfigured
	// (or hostile) URL could hand us another provider's endpoints.
	if strings.TrimSuffix(doc.Issuer, "/") != issuer {
		return nil, fmt.Errorf("%w: document issuer %q does not match %q", ErrDiscovery, doc.Issuer, issuer)
	}
	if doc.AuthURL == "" || doc.TokenURL == "" || doc.JWKSURL == "" {
		return nil, fmt.Errorf("%w: document is missing required endpoints", ErrDiscovery)
	}

	p = &Provider{
		Issuer:       issuer,
		AuthURL:      doc.AuthURL,
		TokenURL:     doc.TokenURL,
		JWKSURL:      doc.JWKSURL,
		discoveredAt: time.Now(),
	}

	c.mu.Lock()
	c.providers[issuer] = p
	c.mu.Unlock()

	return p, nil
}

// AuthCodeURL builds the URL the browser is redirected to in order to log in.
// PKCE is always used, so an intercepted code cannot be redeemed without the
// verifier.
func (p *Provider) AuthCodeURL(clientID, redirectURI, state, nonce, codeChallenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")

	sep := "?"
	if strings.Contains(p.AuthURL, "?") {
		sep = "&"
	}

	return p.AuthURL + sep + q.Encode()
}

// tokenResponse is the subset of the token endpoint response we need.
type tokenResponse struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error"`
	ErrDesc string `json:"error_description"`
}

// Exchange redeems an authorization code for an ID token.
func (c *Client) Exchange(ctx context.Context, p *Provider, clientID, clientSecret, redirectURI, code, codeVerifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTokenExchange, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Prefer client_secret_basic; it is the default every provider supports and
	// keeps the secret out of the request body.
	if clientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTokenExchange, err)
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return "", fmt.Errorf("%w: decode: %s", ErrTokenExchange, err)
	}

	if tr.Error != "" {
		// The provider's error text can quote the request; never echo the secret.
		return "", fmt.Errorf("%w: %s", ErrTokenExchange, tr.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: token endpoint returned %d", ErrTokenExchange, resp.StatusCode)
	}
	if tr.IDToken == "" {
		return "", ErrNoIDToken
	}

	return tr.IDToken, nil
}

// jwksDoc is a JSON Web Key Set restricted to RSA signing keys.
type jwksDoc struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// keyFor returns the provider's public key for kid, refreshing the JWKS when the
// kid is unknown so that provider key rotation does not need a restart.
func (c *Client) keyFor(ctx context.Context, p *Provider, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	key, ok := p.keys[kid]
	fresh := time.Since(p.keysFetchedAt) < c.cacheTTL
	p.mu.Unlock()

	if ok {
		return key, nil
	}
	if fresh && len(p.keys) > 0 && kid == "" {
		return nil, fmt.Errorf("%w: id_token has no kid", ErrVerify)
	}

	keys, err := c.fetchJWKS(ctx, p.JWKSURL)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.keys = keys
	p.keysFetchedAt = time.Now()
	key, ok = p.keys[kid]
	p.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("%w: no key for kid %q", ErrVerify, kid)
	}

	return key, nil
}

func (c *Client) fetchJWKS(ctx context.Context, jwksURL string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: jwks: %s", ErrVerify, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: jwks: %s", ErrVerify, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: jwks returned %d", ErrVerify, resp.StatusCode)
	}

	var doc jwksDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: jwks decode: %s", ErrVerify, err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}

		pub, err := rsaPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: jwks has no usable RS256 keys", ErrVerify)
	}

	return keys, nil
}

// rsaPublicKey rebuilds a public key from the base64url modulus and exponent.
func rsaPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(nB64, "="))
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}

	e, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(eB64, "="))
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	if len(n) == 0 || len(e) == 0 {
		return nil, errors.New("empty modulus or exponent")
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}
