package providers

import (
	"net/http"
	"time"
)

// httpClient is shared by every HTTP-based provider. Alerts are delivered to
// arbitrary customer-controlled endpoints; the timeout keeps one hung endpoint
// from pinning the alert-dispatch worker forever (http.DefaultClient has no
// timeout at all).
var httpClient = &http.Client{Timeout: 10 * time.Second}
