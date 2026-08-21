package ingestapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jkarage/logingestor/business/domain/rejectbus"
)

// readLimited reads up to max bytes from r, erroring if the body exceeds it.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	limited := io.LimitReader(r, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("request body exceeds %d bytes", max)
	}
	return data, nil
}

// parseRecords decodes the body as either newline-delimited JSON or a JSON
// array (or a single JSON object). NDJSON parsing is line-tolerant: a malformed
// line is reported as a RecordError by its line index and the rest proceed.
func parseRecords(contentType string, body []byte) ([]BulkRecord, []RecordError) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}

	isNDJSON := strings.Contains(strings.ToLower(contentType), "ndjson")
	if !isNDJSON && trimmed[0] == '[' {
		// JSON array.
		var arr []BulkRecord
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			// The whole document is the offending record here: there are no lines
			// to blame individually.
			return nil, []RecordError{{
				Index: 0, Error: fmt.Sprintf("invalid JSON array: %v", err),
				Kind: rejectbus.KindParse, Payload: string(trimmed),
			}}
		}
		return arr, nil
	}

	if !isNDJSON && trimmed[0] == '{' && !bytes.Contains(trimmed, []byte("\n")) {
		// Single JSON object.
		var one BulkRecord
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, []RecordError{{
				Index: 0, Error: fmt.Sprintf("invalid JSON object: %v", err),
				Kind: rejectbus.KindParse, Payload: string(trimmed),
			}}
		}
		return []BulkRecord{one}, nil
	}

	// NDJSON: one JSON object per line.
	var records []BulkRecord
	var errs []RecordError
	lines := bytes.Split(trimmed, []byte("\n"))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec BulkRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			errs = append(errs, RecordError{
				Index: i, Error: fmt.Sprintf("invalid JSON: %v", err),
				Kind: rejectbus.KindParse, Payload: string(line),
			})
			continue
		}
		records = append(records, rec)
	}
	return records, errs
}

// normalizeLevel upper-cases a client-supplied level string for ParseLevel.
func normalizeLevel(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// parseTimestamp parses an optional timestamp string (RFC3339, then JS
// millisecond format). A nil or unparseable value falls back to now.
func parseTimestamp(ts *string, now time.Time) time.Time {
	if ts == nil || *ts == "" {
		return now
	}
	if t, err := time.Parse(time.RFC3339, *ts); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z07:00", *ts); err == nil {
		return t.UTC()
	}
	return now
}
