package sourceapp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
)

// NewSource is the request body for creating a source.
type NewSource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`

	// ExpiresInDays optionally bounds the key's lifetime. Omitted or 0 means the
	// key never expires, which is the pre-existing behaviour.
	ExpiresInDays int `json:"expiresInDays"`
}

// Decode implements the web.Decoder interface.
func (app *NewSource) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// SourceCreated is returned by POST /v1/orgs/{org_id}/sources. It is the only
// time the raw ingest key is ever returned.
type SourceCreated struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
	IsActive  bool   `json:"isActive"`
	CreatedAt string `json:"createdAt"`
	IngestKey string `json:"ingestKey"`
	KeyPrefix string `json:"keyPrefix"`

	// ExpiresAt is nil when the key never expires.
	ExpiresAt *string `json:"expiresAt"`
}

// Encode implements the web.Encoder interface.
func (app SourceCreated) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// HTTPStatus returns 201 Created for source creation.
func (app SourceCreated) HTTPStatus() int { return http.StatusCreated }

// Source is the API representation of a source (never includes the raw key).
type Source struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	ProjectID  string  `json:"projectId"`
	IsActive   bool    `json:"isActive"`
	KeyPrefix  string  `json:"keyPrefix"`
	LastSeenAt *string `json:"lastSeenAt"`
	CreatedAt  string  `json:"createdAt"`

	// ExpiresAt is nil when the key never expires. Expired is derived so a
	// client does not have to compare clocks to render key health.
	ExpiresAt *string `json:"expiresAt"`
	Expired   bool    `json:"expired"`

	// Health over the last 24 hours, so the list can be rendered without a
	// request per row. HealthStatus is one of disconnected, expired,
	// never_connected, silent, degraded, healthy.
	HealthStatus string  `json:"healthStatus"`
	Events24h    int64   `json:"events24h"`
	Errors24h    int64   `json:"errors24h"`
	Dropped24h   int64   `json:"dropped24h"`
	ErrorRate24h float64 `json:"errorRate24h"`
}

// Sources is the list response shape: { "sources": [ ... ] }.
type Sources struct {
	Sources []Source `json:"sources"`
}

// Encode implements the web.Encoder interface.
func (app Sources) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// Disconnected is returned by DELETE (soft-disable).
type Disconnected struct {
	Disconnected bool `json:"disconnected"`
}

// Encode implements the web.Encoder interface.
func (app Disconnected) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// RotatedKey is returned by the rotate-key endpoint.
type RotatedKey struct {
	IngestKey string `json:"ingestKey"`
	KeyPrefix string `json:"keyPrefix"`
}

// Encode implements the web.Encoder interface.
func (app RotatedKey) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// SourceHealth is the detail response for one source, adding the hourly shape of
// the window to the same figures the list carries.
type SourceHealth struct {
	SourceID     string  `json:"sourceId"`
	Status       string  `json:"status"`
	IsActive     bool    `json:"isActive"`
	Expired      bool    `json:"expired"`
	ExpiresAt    *string `json:"expiresAt"`
	LastSeenAt   *string `json:"lastSeenAt"`
	WindowStart  string  `json:"windowStart"`
	WindowEnd    string  `json:"windowEnd"`
	Events24h    int64   `json:"events24h"`
	Errors24h    int64   `json:"errors24h"`
	Dropped24h   int64   `json:"dropped24h"`
	ErrorRate24h float64 `json:"errorRate24h"`

	// Buckets always covers every hour of the window, zero-filled, so a client
	// can plot it without normalising absent hours.
	Buckets []HealthBucket `json:"buckets"`
}

// HealthBucket is one hour of a source's ingest.
type HealthBucket struct {
	Hour    string `json:"hour"`
	Events  int64  `json:"events"`
	Errors  int64  `json:"errors"`
	Dropped int64  `json:"dropped"`
}

// Encode implements the web.Encoder interface.
func (app SourceHealth) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

func toAppSource(bus sourcebus.Source) Source {
	s := Source{
		ID:        bus.ID.String(),
		Kind:      bus.Kind,
		Name:      bus.Name,
		ProjectID: bus.ProjectID.String(),
		IsActive:  bus.IsActive,
		KeyPrefix: bus.KeyPrefix,
		CreatedAt: bus.CreatedAt.Format(time.RFC3339),
	}
	if bus.LastSeenAt != nil {
		ls := bus.LastSeenAt.Format(time.RFC3339)
		s.LastSeenAt = &ls
	}
	if bus.ExpiresAt != nil {
		v := bus.ExpiresAt.Format(time.RFC3339)
		s.ExpiresAt = &v
	}
	s.Expired = bus.Expired(time.Now())

	return s
}

// withHealth attaches the derived health for a source to its API row.
func withHealth(s Source, h sourcebus.Health) Source {
	s.HealthStatus = string(h.Status)
	s.Events24h = h.Events
	s.Errors24h = h.Errors
	s.Dropped24h = h.Dropped
	s.ErrorRate24h = h.ErrorRate

	return s
}

// toAppSources maps sources onto the list response, deriving each one's health
// from counters keyed by source ID. A source missing from counters had no ingest
// in the window, which is a health state rather than missing data.
func toAppSources(buses []sourcebus.Source, counters map[uuid.UUID]sourcebus.HealthCounters, now time.Time) Sources {
	out := make([]Source, len(buses))
	for i, b := range buses {
		out[i] = withHealth(toAppSource(b), b.Health(now, counters[b.ID]))
	}

	return Sources{Sources: out}
}
