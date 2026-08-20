// Package annotationapp maintains the app layer api for log annotations.
package annotationapp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jkarage/logingestor/business/domain/annotationbus"
)

// Annotation is a note as returned by the API.
type Annotation struct {
	ID        string  `json:"id"`
	OrgID     string  `json:"orgId"`
	ProjectID string  `json:"projectId"`
	LogID     *string `json:"logId"`
	TS        string  `json:"ts"`
	Body      string  `json:"body"`
	CreatedBy string  `json:"createdBy"`

	// CanEdit tells the client whether to offer edit and delete, so it does not
	// have to reimplement the ownership rule.
	CanEdit bool `json:"canEdit"`

	DateCreated string `json:"dateCreated"`
	DateUpdated string `json:"dateUpdated"`
}

// Encode implements the encoder interface.
func (app Annotation) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// created wraps an annotation so a successful create returns 201.
type created struct{ Annotation }

// HTTPStatus implements the web status interface.
func (created) HTTPStatus() int { return http.StatusCreated }

// Annotations is the list response shape.
type Annotations struct {
	Annotations []Annotation `json:"annotations"`
}

// Encode implements the encoder interface.
func (app Annotations) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// NewAnnotation is the request body for creating a note.
//
// Anchor: logId attaches the note to a log line, ts pins it to a moment. Given
// both, logId wins and ts is taken from the log itself, so the note cannot sit
// somewhere other than the line it describes.
type NewAnnotation struct {
	ProjectID string `json:"projectId"`
	LogID     string `json:"logId"`
	TS        string `json:"ts"`
	Body      string `json:"body"`
}

// Decode implements the decoder interface.
func (app *NewAnnotation) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

// UpdateAnnotation is the request body for editing a note. The anchor is
// immutable, so only the text can change.
type UpdateAnnotation struct {
	Body *string `json:"body"`
}

// Decode implements the decoder interface.
func (app *UpdateAnnotation) Decode(data []byte) error {
	return json.Unmarshal(data, app)
}

func toAppAnnotation(a annotationbus.Annotation, canEdit bool) Annotation {
	out := Annotation{
		ID:          a.ID.String(),
		OrgID:       a.OrgID.String(),
		ProjectID:   a.ProjectID.String(),
		TS:          a.TS.UTC().Format(time.RFC3339),
		Body:        a.Body,
		CreatedBy:   a.CreatedBy.String(),
		CanEdit:     canEdit,
		DateCreated: a.DateCreated.Format(time.RFC3339),
		DateUpdated: a.DateUpdated.Format(time.RFC3339),
	}

	if a.LogID != nil {
		s := a.LogID.String()
		out.LogID = &s
	}

	return out
}
