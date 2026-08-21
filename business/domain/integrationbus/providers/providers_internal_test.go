package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// samplePayload is one ERROR alert, the case every provider has to render.
func samplePayload() integrationbus.AlertPayload {
	return integrationbus.AlertPayload{
		ProjectName: "checkout",
		Level:       "ERROR",
		Message:     "payment gateway timeout",
		Source:      "billing",
		LogID:       "a1b2c3d4-0000-0000-0000-000000000000",
		Timestamp:   time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC),
	}
}

// capture records what a provider actually sent.
type capture struct {
	method      string
	path        string
	contentType string
	auth        string
	apiKey      string
	body        string
}

// server returns a test server that records one request and replies with the
// given status and body.
func server(t *testing.T, got *capture, status int, reply string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		got.method = r.Method
		got.path = r.URL.Path
		got.contentType = r.Header.Get("Content-Type")
		got.auth = r.Header.Get("Authorization")
		got.apiKey = r.Header.Get("apiKey")
		got.body = string(b)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// Every provider must refuse to send when a required credential is absent, and
// must do it without making a request — a half-configured integration should
// fail at the connection test, not silently post to nowhere.
func Test_Providers_RequireTheirCredentials(t *testing.T) {
	cases := []struct {
		name   string
		caller integrationbus.Caller
		creds  map[string]string
		want   string
	}{
		{"msteams without a url", NewMSTeams(), nil, "webhookUrl"},
		{"googlechat without a url", NewGoogleChat(), nil, "webhookUrl"},
		{"mattermost without a url", NewMattermost(), nil, "webhookUrl"},
		{"whatsapp without a phone number id", NewWhatsApp(), map[string]string{"accessToken": "t", "to": "+1"}, "phoneNumberId"},
		{"whatsapp without a token", NewWhatsApp(), map[string]string{"phoneNumberId": "1", "to": "+1"}, "accessToken"},
		{"whatsapp without a recipient", NewWhatsApp(), map[string]string{"phoneNumberId": "1", "accessToken": "t"}, "to"},
		{"github without an owner", NewGitHub(), map[string]string{"repo": "r", "token": "t"}, "owner"},
		{"github without a repo", NewGitHub(), map[string]string{"owner": "o", "token": "t"}, "repo"},
		{"github without a token", NewGitHub(), map[string]string{"owner": "o", "repo": "r"}, "token"},
		{"linear without an api key", NewLinear(), map[string]string{"teamId": "t"}, "apiKey"},
		{"linear without a team", NewLinear(), map[string]string{"apiKey": "k"}, "teamId"},
		{"africastalking without a username", NewAfricasTalking(), map[string]string{"apiKey": "k", "to": "+1"}, "username"},
		{"africastalking without an api key", NewAfricasTalking(), map[string]string{"username": "u", "to": "+1"}, "apiKey"},
		{"africastalking without a recipient", NewAfricasTalking(), map[string]string{"username": "u", "apiKey": "k"}, "to"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.caller.Send(context.Background(), c.creds, samplePayload())
			if err == nil {
				t.Fatalf("sent with a missing credential")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name the missing credential %q", err, c.want)
			}
		})
	}
}

// The three webhook providers each post the alert to the URL they were given.
// What matters is that the level, the project and the message all survive into
// the body — a card that renders without them is useless in a busy channel.
func Test_WebhookProviders_PostTheAlert(t *testing.T) {
	cases := []struct {
		name   string
		build  func(url string) (integrationbus.Caller, map[string]string)
		status int
	}{
		{
			name: "microsoft teams accepts 202 from a workflow",
			build: func(u string) (integrationbus.Caller, map[string]string) {
				return NewMSTeams(), map[string]string{"webhookUrl": u}
			},
			status: http.StatusAccepted,
		},
		{
			name: "google chat",
			build: func(u string) (integrationbus.Caller, map[string]string) {
				return NewGoogleChat(), map[string]string{"webhookUrl": u}
			},
			status: http.StatusOK,
		},
		{
			name: "mattermost",
			build: func(u string) (integrationbus.Caller, map[string]string) {
				return NewMattermost(), map[string]string{"webhookUrl": u}
			},
			status: http.StatusOK,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got capture
			srv := server(t, &got, c.status, "{}")

			caller, creds := c.build(srv.URL)
			if err := caller.Send(context.Background(), creds, samplePayload()); err != nil {
				t.Fatalf("send: %v", err)
			}

			if got.method != http.MethodPost {
				t.Errorf("method = %s, want POST", got.method)
			}
			if !strings.HasPrefix(got.contentType, "application/json") {
				t.Errorf("content type = %q, want JSON", got.contentType)
			}
			if !json.Valid([]byte(got.body)) {
				t.Fatalf("body is not valid JSON: %s", got.body)
			}
			for _, want := range []string{"ERROR", "checkout", "payment gateway timeout", "billing"} {
				if !strings.Contains(got.body, want) {
					t.Errorf("body does not carry %q: %s", want, got.body)
				}
			}
		})
	}
}

// A channel that rejects the post is a failure, so the alert is retried rather
// than marked delivered.
func Test_WebhookProviders_ReportRejection(t *testing.T) {
	for _, c := range []struct {
		name   string
		caller integrationbus.Caller
	}{
		{"msteams", NewMSTeams()},
		{"googlechat", NewGoogleChat()},
		{"mattermost", NewMattermost()},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got capture
			srv := server(t, &got, http.StatusBadRequest, `{"error":"bad payload"}`)

			err := c.caller.Send(context.Background(), map[string]string{"webhookUrl": srv.URL}, samplePayload())
			if err == nil {
				t.Fatalf("a 400 was treated as delivered")
			}
			if !strings.Contains(err.Error(), "400") {
				t.Errorf("error %q does not carry the status", err)
			}
		})
	}
}

// Mattermost only sends a channel override when one is configured, because a
// webhook that was not created with override permission rejects the whole post.
func Test_Mattermost_ChannelOverrideIsOptional(t *testing.T) {
	var got capture
	srv := server(t, &got, http.StatusOK, "ok")

	if err := NewMattermost().Send(context.Background(), map[string]string{"webhookUrl": srv.URL}, samplePayload()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if strings.Contains(got.body, "channel") {
		t.Errorf("channel was sent without being configured: %s", got.body)
	}

	if err := NewMattermost().Send(context.Background(), map[string]string{"webhookUrl": srv.URL, "channel": "alerts"}, samplePayload()); err != nil {
		t.Fatalf("send with channel: %v", err)
	}
	if !strings.Contains(got.body, `"channel":"alerts"`) {
		t.Errorf("configured channel was not sent: %s", got.body)
	}
}

// Teams colours the heading by level, which is the only thing that makes an
// ERROR stand out from an INFO in a channel full of cards.
func Test_MSTeams_ColoursByLevel(t *testing.T) {
	for level, want := range map[string]string{"ERROR": "attention", "WARN": "warning", "INFO": "default"} {
		var got capture
		srv := server(t, &got, http.StatusOK, "1")

		payload := samplePayload()
		payload.Level = level

		if err := NewMSTeams().Send(context.Background(), map[string]string{"webhookUrl": srv.URL}, payload); err != nil {
			t.Fatalf("send %s: %v", level, err)
		}
		if !strings.Contains(got.body, `"color":"`+want+`"`) {
			t.Errorf("%s card colour is not %q: %s", level, want, got.body)
		}
		// The card has to be wrapped as an attachment or Workflows drops it.
		if !strings.Contains(got.body, "application/vnd.microsoft.card.adaptive") {
			t.Errorf("card is not wrapped as an adaptive card attachment")
		}
	}
}

// WhatsApp sends free text only when no template is configured. Outside the
// 24-hour window Meta rejects free text, so the template path is the one that
// works for alerting and it must actually be taken.
func Test_WhatsApp_TextVersusTemplate(t *testing.T) {
	var got capture
	srv := server(t, &got, http.StatusOK, `{"messages":[{"id":"wamid.X"}]}`)

	original := whatsappBase
	whatsappBase = srv.URL
	t.Cleanup(func() { whatsappBase = original })

	creds := map[string]string{"phoneNumberId": "12345", "accessToken": "tok", "to": "+255700000000"}

	if err := NewWhatsApp().Send(context.Background(), creds, samplePayload()); err != nil {
		t.Fatalf("send text: %v", err)
	}
	if got.path != "/12345/messages" {
		t.Errorf("path = %q, want /12345/messages", got.path)
	}
	if got.auth != "Bearer tok" {
		t.Errorf("authorization = %q, want a bearer token", got.auth)
	}
	if !strings.Contains(got.body, `"type":"text"`) {
		t.Errorf("expected a text message: %s", got.body)
	}

	creds["templateName"] = "streamlogia_alert"
	if err := NewWhatsApp().Send(context.Background(), creds, samplePayload()); err != nil {
		t.Fatalf("send template: %v", err)
	}
	if !strings.Contains(got.body, `"type":"template"`) {
		t.Errorf("expected a template message: %s", got.body)
	}
	if !strings.Contains(got.body, `"name":"streamlogia_alert"`) {
		t.Errorf("template name missing: %s", got.body)
	}
	// The default language keeps a one-field configuration working.
	if !strings.Contains(got.body, `"code":"en"`) {
		t.Errorf("expected the default language: %s", got.body)
	}
	if !strings.Contains(got.body, "payment gateway timeout") {
		t.Errorf("the alert text is not in the template parameter: %s", got.body)
	}
}

// GitHub Enterprise lives on the customer's host, so the base URL is a
// credential and the path has to be built from it correctly.
func Test_GitHub_CreatesAnIssue(t *testing.T) {
	var got capture
	srv := server(t, &got, http.StatusCreated, `{"number":42}`)

	creds := map[string]string{
		"owner": "my-org", "repo": "my-service", "token": "ghp_x",
		"apiBaseUrl": srv.URL + "/api/v3/", // trailing slash on purpose
		"labels":     "incident, logs ,",
	}

	if err := NewGitHub().Send(context.Background(), creds, samplePayload()); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got.path != "/api/v3/repos/my-org/my-service/issues" {
		t.Errorf("path = %q", got.path)
	}
	if got.auth != "Bearer ghp_x" {
		t.Errorf("authorization = %q", got.auth)
	}

	var body struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if !strings.Contains(body.Title, "ERROR") || !strings.Contains(body.Title, "payment gateway timeout") {
		t.Errorf("title = %q", body.Title)
	}
	if !strings.Contains(body.Body, "a1b2c3d4-0000-0000-0000-000000000000") {
		t.Errorf("issue body does not carry the log id: %q", body.Body)
	}
	// Blank entries in the comma list are dropped rather than sent as "".
	if len(body.Labels) != 2 || body.Labels[0] != "incident" || body.Labels[1] != "logs" {
		t.Errorf("labels = %v, want [incident logs]", body.Labels)
	}
}

func Test_GitHub_ReportsRejection(t *testing.T) {
	var got capture
	srv := server(t, &got, http.StatusForbidden, `{"message":"Issues are disabled for this repo"}`)

	err := NewGitHub().Send(context.Background(), map[string]string{
		"owner": "o", "repo": "r", "token": "t", "apiBaseUrl": srv.URL,
	}, samplePayload())

	if err == nil {
		t.Fatalf("a 403 was treated as success")
	}
	// The reason has to survive: "issues are disabled" and "no such repo" need
	// different fixes.
	if !strings.Contains(err.Error(), "Issues are disabled") {
		t.Errorf("error %q drops the reason", err)
	}
}

// Linear answers 200 even when the mutation failed, so the body decides.
func Test_Linear_ChecksTheGraphQLBody(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantErr string
	}{
		{
			name:  "created",
			reply: `{"data":{"issueCreate":{"success":true,"issue":{"identifier":"ENG-12"}}}}`,
		},
		{
			name:    "graphql error at 200",
			reply:   `{"errors":[{"message":"Team not found"}]}`,
			wantErr: "Team not found",
		},
		{
			name:    "mutation reported failure",
			reply:   `{"data":{"issueCreate":{"success":false}}}`,
			wantErr: "not created",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got capture
			srv := server(t, &got, http.StatusOK, c.reply)

			original := linearEndpoint
			linearEndpoint = srv.URL
			t.Cleanup(func() { linearEndpoint = original })

			err := NewLinear().Send(context.Background(), map[string]string{"apiKey": "lin_api_x", "teamId": "team-1"}, samplePayload())

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				// A personal API key is sent raw; "Bearer" is rejected by Linear.
				if got.auth != "lin_api_x" {
					t.Errorf("authorization = %q, want the raw key", got.auth)
				}
				if !strings.Contains(got.body, "issueCreate") || !strings.Contains(got.body, "team-1") {
					t.Errorf("mutation body = %s", got.body)
				}
				return
			}

			if err == nil {
				t.Fatalf("a failed mutation was treated as delivered")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not mention %q", err, c.wantErr)
			}
		})
	}
}

// Africa's Talking is form encoded and reports per-recipient outcomes inside a
// 201, so a refused number must not read as delivered.
func Test_AfricasTalking_ChecksRecipientStatus(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantErr string
	}{
		{
			name:  "queued",
			reply: `{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"number":"+254700000000","status":"Success","statusCode":101}]}}`,
		},
		{
			name:    "refused sender id",
			reply:   `{"SMSMessageData":{"Message":"InvalidSenderId","Recipients":[{"number":"+254700000000","status":"InvalidSenderId","statusCode":402}]}}`,
			wantErr: "402",
		},
		{
			name:    "nothing queued",
			reply:   `{"SMSMessageData":{"Message":"InsufficientBalance","Recipients":[]}}`,
			wantErr: "InsufficientBalance",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got capture
			srv := server(t, &got, http.StatusCreated, c.reply)

			original := atLiveBase
			atLiveBase = srv.URL
			t.Cleanup(func() { atLiveBase = original })

			err := NewAfricasTalking().Send(context.Background(), map[string]string{
				"username": "myapp", "apiKey": "atsk_x", "to": "+254700000000", "from": "MYAPP",
			}, samplePayload())

			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				if got.path != "/version1/messaging" {
					t.Errorf("path = %q", got.path)
				}
				if got.apiKey != "atsk_x" {
					t.Errorf("apiKey header = %q", got.apiKey)
				}
				if got.contentType != "application/x-www-form-urlencoded" {
					t.Errorf("content type = %q, want form encoding", got.contentType)
				}
				for _, want := range []string{"username=myapp", "from=MYAPP", "payment+gateway+timeout"} {
					if !strings.Contains(got.body, want) {
						t.Errorf("form does not carry %q: %s", want, got.body)
					}
				}
				return
			}

			if err == nil {
				t.Fatalf("a refusal was treated as delivered")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q does not mention %q", err, c.wantErr)
			}
		})
	}
}

// The sandbox is a different host that only accepts the username "sandbox";
// sending sandbox credentials to the live host fails with an opaque 401.
func Test_AfricasTalking_SandboxHost(t *testing.T) {
	var got capture
	srv := server(t, &got, http.StatusCreated, `{"SMSMessageData":{"Message":"ok","Recipients":[{"number":"+1","status":"Success","statusCode":101}]}}`)

	originalSandbox, originalLive := atSandboxBase, atLiveBase
	atSandboxBase = srv.URL
	atLiveBase = "http://live.invalid"
	t.Cleanup(func() { atSandboxBase, atLiveBase = originalSandbox, originalLive })

	// The username alone is enough to pick the sandbox.
	if err := NewAfricasTalking().Send(context.Background(), map[string]string{
		"username": "sandbox", "apiKey": "k", "to": "+1",
	}, samplePayload()); err != nil {
		t.Fatalf("send with the sandbox username: %v", err)
	}

	// So is the explicit flag.
	if err := NewAfricasTalking().Send(context.Background(), map[string]string{
		"username": "myapp", "apiKey": "k", "to": "+1", "sandbox": "TRUE",
	}, samplePayload()); err != nil {
		t.Fatalf("send with the sandbox flag: %v", err)
	}
}
