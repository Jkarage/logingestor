package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jkarage/logingestor/foundation/web"
)

func noopLog(ctx context.Context, msg string, args ...any) {}

type panicEncoder struct{ inner web.Encoder }

func (p *panicEncoder) Encode() web.Encoder { return p.inner }

func nilEncoder() *panicEncoder { return nil }

// Test_NoMidPanicRecovery verifies that routes registered without the
// application middleware still recover from panics. Before this protection,
// a panic on a no-mid route (e.g. /v1/readiness with an unwired DB) escaped
// to net/http's own recover, killing the connection and bypassing our logs.
func Test_NoMidPanicRecovery(t *testing.T) {
	app := web.NewApp(noopLog, nil)

	app.HandlerFuncNoMid(http.MethodGet, "v1", "/boom", func(ctx context.Context, r *http.Request) web.Encoder {
		return nilEncoder().Encode() // nil pointer dereference, like the unwired-DB incident
	})

	app.RawHandlerFuncNoMid(http.MethodGet, "v1", "/rawboom", func(w http.ResponseWriter, r *http.Request) {
		panic("raw handler panic")
	})

	srv := httptest.NewServer(app)
	defer srv.Close()

	for _, path := range []string{"/v1/boom", "/v1/rawboom"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: request failed (panic escaped to net/http?): %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("%s: expected 500, got %d: %s", path, resp.StatusCode, body)
		}
	}
}
