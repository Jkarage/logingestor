package checkapp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jkarage/logingestor/app/domain/checkapp"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Test_ReadinessNilDB locks down a production incident: the readiness route is
// registered with HandlerFuncNoMid, so it has no panic-recovery middleware. When
// main.go was wired without a DB, every probe of /v1/readiness panicked with a
// nil pointer dereference that escaped to net/http's own recover. A nil DB must
// surface as a 500 status failure, never a panic.
func Test_ReadinessNilDB(t *testing.T) {
	lg := logger.New(io.Discard, logger.LevelInfo, "TEST", nil)

	app := web.NewApp(lg.Info, nil)

	checkapp.Routes(app, checkapp.Config{
		Build: "test",
		Log:   lg,
		DB:    nil,
	})

	srv := httptest.NewServer(app)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/readiness")
	if err != nil {
		t.Fatalf("request failed (handler panicked?): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 500, got %d: %s", resp.StatusCode, body)
	}
}
