package sourceapp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jkarage/logingestor/business/domain/sourcebus"
)

// NewSource is the request body for creating a source.
type NewSource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
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
	return s
}

func toAppSources(buses []sourcebus.Source) Sources {
	out := make([]Source, len(buses))
	for i, b := range buses {
		out[i] = toAppSource(b)
	}
	return Sources{Sources: out}
}
