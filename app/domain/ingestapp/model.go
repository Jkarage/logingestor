package ingestapp

import (
	"encoding/json"
	"net/http"
)

// BulkRecord is a single inbound infrastructure-log record on the HTTP bulk
// endpoint. Every field except message is optional.
type BulkRecord struct {
	Level           string         `json:"level"`
	Message         string         `json:"message"`
	Source          string         `json:"source"`
	Timestamp       *string        `json:"ts"`
	Tags            []string       `json:"tags"`
	Host            string         `json:"host"`
	Container       string         `json:"container"`
	Pod             string         `json:"pod"`
	Namespace       string         `json:"namespace"`
	Cluster         string         `json:"cluster"`
	Unit            string         `json:"unit"`
	Facility        string         `json:"facility"`
	Region          string         `json:"region"`
	CloudResourceID string         `json:"cloudResourceId"`
	Attributes      map[string]any `json:"attributes"`
}

// RecordError reports a rejected record by its index in the request.
type RecordError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// BulkResponse is returned by POST /v1/ingest/bulk. Partial success is allowed:
// accepted + rejected records are reported separately.
type BulkResponse struct {
	Accepted int           `json:"accepted"`
	Rejected int           `json:"rejected"`
	Errors   []RecordError `json:"errors,omitempty"`
}

// Encode implements the web.Encoder interface.
func (r BulkResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// HTTPStatus returns 202 Accepted — ingestion is best-effort and asynchronous
// from the agent's perspective.
func (r BulkResponse) HTTPStatus() int { return http.StatusAccepted }
