package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test_clientInfoFrom(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		wantIP     string
	}{
		{
			name:       "falls back to RemoteAddr with the port stripped",
			remoteAddr: "203.0.113.7:54321",
			wantIP:     "203.0.113.7",
		},
		{
			// Behind a proxy the left-most XFF entry is the original client.
			name:       "prefers the left-most X-Forwarded-For entry",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.1, 10.0.0.2"},
			wantIP:     "203.0.113.7",
		},
		{
			name:       "trims whitespace in X-Forwarded-For",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "   203.0.113.9   "},
			wantIP:     "203.0.113.9",
		},
		{
			name:       "uses X-Real-IP when X-Forwarded-For is absent",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Real-IP": "203.0.113.11"},
			wantIP:     "203.0.113.11",
		},
		{
			name:       "X-Forwarded-For wins over X-Real-IP",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.7", "X-Real-IP": "203.0.113.11"},
			wantIP:     "203.0.113.7",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[2001:db8::1]:443",
			wantIP:     "2001:db8::1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.RemoteAddr = c.remoteAddr
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}

			if got := clientInfoFrom(r); got.IP != c.wantIP {
				t.Errorf("IP = %q, want %q", got.IP, c.wantIP)
			}
		})
	}
}

// A hostile user agent must not become an unbounded column write.
func Test_clientInfoFrom_TruncatesUserAgent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "203.0.113.7:1234"
	r.Header.Set("User-Agent", strings.Repeat("A", 5000))

	if got := len(clientInfoFrom(r).UserAgent); got != 512 {
		t.Errorf("user agent length = %d, want 512", got)
	}
}

// Code with no request in scope (background workers) must read the zero value
// rather than panic.
func Test_GetClientInfo_NoRequest(t *testing.T) {
	if ci := GetClientInfo(context.Background()); ci.IP != "" || ci.UserAgent != "" {
		t.Errorf("expected zero ClientInfo, got %+v", ci)
	}
}

func Test_setGetClientInfo_RoundTrip(t *testing.T) {
	want := ClientInfo{IP: "203.0.113.7", UserAgent: "curl/8"}

	if got := GetClientInfo(setClientInfo(context.Background(), want)); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
