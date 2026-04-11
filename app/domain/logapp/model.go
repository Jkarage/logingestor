package logapp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/logbus"
)

// =============================================================================
// Ingest

// IngestEntry is a single entry from the ingest request body.
// The frontend sends "projectId"; we return "pid" everywhere.
type IngestEntry struct {
	ProjectID string         `json:"projectId"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Source    string         `json:"source"`
	Timestamp *string        `json:"timestamp"` //Todo(update this to use value semantics)
	Tags      []string       `json:"tags"`
	Meta      map[string]any `json:"meta"`
}

// IngestRequest accepts either a single object or an array.
type IngestRequest []IngestEntry

// Decode implements the web.Decoder interface, accepting both object and array.
func (r *IngestRequest) Decode(data []byte) error {
	// Try array first.
	var arr []IngestEntry
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = IngestRequest(arr)
		return nil
	}

	// Fall back to single object.
	var single IngestEntry
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}

	*r = IngestRequest{single}
	return nil
}

func toBusNewLogs(entries IngestRequest) ([]logbus.NewLog, *errs.FieldErrors) {
	var fieldErrs errs.FieldErrors
	news := make([]logbus.NewLog, 0, len(entries))

	for i, e := range entries {
		projectID, err := uuid.Parse(e.ProjectID)
		if err != nil {
			fieldErrs.Add(fmt.Sprintf("[%d].projectId", i), err)
			continue
		}

		lvl, err := logbus.ParseLevel(e.Level)
		if err != nil {
			fieldErrs.Add(fmt.Sprintf("[%d].level", i), err)
			continue
		}

		if e.Message == "" {
			fieldErrs.Add(fmt.Sprintf("[%d].message", i), fmt.Errorf("message is required"))
			continue
		}

		if e.Source == "" {
			fieldErrs.Add(fmt.Sprintf("[%d].source", i), fmt.Errorf("source is required"))
			continue
		}

		ts := time.Now().UTC()
		if e.Timestamp != nil {
			parsed, err := time.Parse(time.RFC3339, *e.Timestamp)
			if err != nil {
				// Fall back to millisecond precision (what browsers/JS typically send).
				parsed, err = time.Parse("2006-01-02T15:04:05.000Z07:00", *e.Timestamp)
			}
			if err != nil {
				fieldErrs.Add(fmt.Sprintf("[%d].ts", i), err)
				continue
			}
			ts = parsed.UTC()
		}

		tags := e.Tags
		if tags == nil {
			tags = []string{}
		}
		meta := e.Meta
		if meta == nil {
			meta = map[string]any{}
		}

		news = append(news, logbus.NewLog{
			ProjectID: projectID,
			Level:     lvl,
			Message:   e.Message,
			Source:    e.Source,
			Timestamp: ts,
			Tags:      tags,
			Meta:      meta,
		})
	}

	if len(fieldErrs) > 0 {
		return nil, &fieldErrs
	}

	return news, nil
}

// IngestResponse is returned by POST /v1/ingest.
type IngestResponse struct {
	Ingested int      `json:"ingested"`
	IDs      []string `json:"ids"`
}

func (r IngestResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// =============================================================================
// Log entry

// LogEntry is the API representation of a log row.
// Note: the frontend expects "pid" (not "projectId"). // TODO: Update the frontend to use project_id.
type LogEntry struct {
	ID        string         `json:"id"`
	PID       string         `json:"pid"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Source    string         `json:"source"`
	Timestamp string         `json:"timestamp"`
	Tags      []string       `json:"tags"`
	Meta      map[string]any `json:"meta"`
}

func toAppLogEntry(bus logbus.Log) LogEntry {
	tags := bus.Tags
	if tags == nil {
		tags = []string{}
	}
	meta := bus.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	return LogEntry{
		ID:        bus.ID.String(),
		PID:       bus.ProjectID.String(),
		Level:     bus.Level.String(),
		Message:   bus.Message,
		Source:    bus.Source,
		Timestamp: bus.Timestamp.UTC().Format(time.RFC3339),
		Tags:      tags,
		Meta:      meta,
	}
}

// =============================================================================
// Query response

// LogsResponse is returned by GET /projects/{project_id}/logs.
type LogsResponse struct {
	Logs       []LogEntry `json:"logs"`
	NextCursor *string    `json:"nextCursor"`
	Total      int        `json:"total"`
}

func (r LogsResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}

// =============================================================================
// Stats response

// StatsResponse is returned by GET /projects/{project_id}/logs/stats.
type StatsResponse map[string]int

func (r StatsResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}
