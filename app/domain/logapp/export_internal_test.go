package logapp

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/logbus"
)

// pagePlan turns a row count into cursor-paged results the way the store does:
// a cursor is the index of the next row, and the last page carries no cursor.
func pagePlan(total, pageSize int) func(cursor string) (logbus.QueryResult, error) {
	all := make([]logbus.Log, total)
	for i := range all {
		all[i] = logbus.Log{
			ID:         uuid.New(),
			ProjectID:  uuid.New(),
			Level:      logbus.LevelInfo,
			Message:    "row",
			Source:     "api",
			SourceType: logbus.SourceTypeApp,
			Timestamp:  time.Unix(int64(1_700_000_000-i), 0).UTC(),
		}
	}

	return func(cursor string) (logbus.QueryResult, error) {
		start := 0
		if cursor != "" {
			n, err := strconv.Atoi(cursor)
			if err != nil {
				return logbus.QueryResult{}, fmt.Errorf("bad cursor %q: %w", cursor, err)
			}
			start = n
		}

		end := min(start+pageSize, len(all))
		page := all[start:end]

		var next *string
		if len(page) == pageSize && end < len(all) {
			c := strconv.Itoa(end)
			next = &c
		}

		return logbus.QueryResult{Logs: page, NextCursor: next}, nil
	}
}

// An export follows the cursor to the end rather than returning one page, which
// is the whole difference between it and the list endpoint.
func Test_writeExport_FollowsEveryPage(t *testing.T) {
	var buf bytes.Buffer

	written, err := writeExport(context.Background(), &buf, pagePlan(2500, 1000), formatNDJSON, MaxExportRows)
	if err != nil {
		t.Fatalf("writeExport: %v", err)
	}

	if written != 2500 {
		t.Errorf("wrote %d rows, want 2500", written)
	}

	lines := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1
	if lines != 2500 {
		t.Errorf("body has %d lines, want 2500", lines)
	}

	// Every line must be a complete JSON object: a consumer reads this with a
	// line-oriented parser and a single truncated line breaks the whole file.
	var entry LogEntry
	first := strings.SplitN(buf.String(), "\n", 2)[0]
	if err := json.Unmarshal([]byte(first), &entry); err != nil {
		t.Fatalf("first line is not valid JSON: %v", err)
	}
	if entry.Message != "row" {
		t.Errorf("message = %q, want %q", entry.Message, "row")
	}
}

// The cap is what keeps one request from becoming an unbounded scan, so it must
// bound the rows written and not merely the pages fetched.
func Test_writeExport_StopsAtTheRowCap(t *testing.T) {
	var buf bytes.Buffer

	written, err := writeExport(context.Background(), &buf, pagePlan(10_000, 1000), formatNDJSON, 1500)
	if err != nil {
		t.Fatalf("writeExport: %v", err)
	}

	if written != 1500 {
		t.Errorf("wrote %d rows, want the cap of 1500", written)
	}

	lines := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1
	if lines != 1500 {
		t.Errorf("body has %d lines, want 1500", lines)
	}
}

// A CSV export carries the header once, in a fixed column order, and every row
// parses — including messages with commas, quotes and newlines in them.
func Test_writeExport_CSVIsParseable(t *testing.T) {
	nasty := logbus.Log{
		ID:         uuid.New(),
		ProjectID:  uuid.New(),
		Level:      logbus.LevelError,
		Message:    `he said "boom", then a newline` + "\nand more",
		Source:     "billing,eu",
		SourceType: logbus.SourceTypeApp,
		Timestamp:  time.Unix(1_700_000_000, 0).UTC(),
		Tags:       []string{"a", "b"},
		Meta:       map[string]any{"orderId": "123"},
	}

	fetch := func(string) (logbus.QueryResult, error) {
		return logbus.QueryResult{Logs: []logbus.Log{nasty}}, nil
	}

	var buf bytes.Buffer
	if _, err := writeExport(context.Background(), &buf, fetch, formatCSV, MaxExportRows); err != nil {
		t.Fatalf("writeExport: %v", err)
	}

	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("export is not valid CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d CSV records, want a header plus one row", len(rows))
	}

	for i, want := range csvHeader {
		if rows[0][i] != want {
			t.Errorf("header[%d] = %q, want %q", i, rows[0][i], want)
		}
	}

	if rows[1][4] != nasty.Message {
		t.Errorf("message round-trip failed: got %q", rows[1][4])
	}
	if rows[1][3] != "billing,eu" {
		t.Errorf("source = %q, want %q", rows[1][3], "billing,eu")
	}
	if rows[1][7] != "a,b" {
		t.Errorf("tags = %q, want %q", rows[1][7], "a,b")
	}
	if !strings.Contains(rows[1][8], `"orderId":"123"`) {
		t.Errorf("meta = %q, want it to carry the JSON payload", rows[1][8])
	}
}

// An empty result still produces a well-formed file, so a client's parser is
// never handed something it has to special-case.
func Test_writeExport_EmptyIsWellFormed(t *testing.T) {
	fetch := func(string) (logbus.QueryResult, error) { return logbus.QueryResult{}, nil }

	var ndjson bytes.Buffer
	if _, err := writeExport(context.Background(), &ndjson, fetch, formatNDJSON, MaxExportRows); err != nil {
		t.Fatalf("ndjson: %v", err)
	}
	if ndjson.Len() != 0 {
		t.Errorf("empty NDJSON export wrote %q, want nothing", ndjson.String())
	}

	var csvOut bytes.Buffer
	if _, err := writeExport(context.Background(), &csvOut, fetch, formatCSV, MaxExportRows); err != nil {
		t.Fatalf("csv: %v", err)
	}
	rows, err := csv.NewReader(&csvOut).ReadAll()
	if err != nil {
		t.Fatalf("empty CSV export is not valid CSV: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("empty CSV export has %d records, want just the header", len(rows))
	}
}

// A page failing mid-stream cannot become an error response — the body is
// already going out — so the rows written so far must survive and the caller
// must learn that it stopped early.
func Test_writeExport_MidStreamFailureKeepsWhatItWrote(t *testing.T) {
	good := pagePlan(3000, 1000)

	calls := 0
	fetch := func(cursor string) (logbus.QueryResult, error) {
		calls++
		if calls == 3 {
			return logbus.QueryResult{}, errors.New("connection reset")
		}
		return good(cursor)
	}

	var buf bytes.Buffer
	written, err := writeExport(context.Background(), &buf, fetch, formatNDJSON, MaxExportRows)

	if err == nil {
		t.Fatalf("writeExport returned no error after a failed page")
	}
	if written != 2000 {
		t.Errorf("wrote %d rows, want the 2000 fetched before the failure", written)
	}

	lines := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1
	if lines != 2000 {
		t.Errorf("body has %d lines, want 2000 flushed before the failure", lines)
	}
}

// A client that hangs up stops the export instead of paging the whole project
// into a closed socket.
func Test_writeExport_StopsWhenTheClientGoesAway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	fetch := pagePlan(10_000, 1000)
	wrapped := func(cursor string) (logbus.QueryResult, error) {
		// Hang up after the first page has been handed over.
		if cursor != "" {
			cancel()
		}
		return fetch(cursor)
	}

	var buf bytes.Buffer
	written, err := writeExport(ctx, &buf, wrapped, formatNDJSON, MaxExportRows)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if written != 2000 {
		t.Errorf("wrote %d rows, want it to stop after the page in flight", written)
	}
}

func Test_parseFormat(t *testing.T) {
	cases := []struct {
		query string
		want  string
		bad   bool
	}{
		{"", formatNDJSON, false},
		{"?format=ndjson", formatNDJSON, false},
		{"?format=NDJSON", formatNDJSON, false},
		{"?format=jsonl", formatNDJSON, false},
		{"?format=csv", formatCSV, false},
		{"?format=xlsx", "", true},
	}

	for _, c := range cases {
		r := httptest.NewRequest("GET", "/v1/projects/x/logs/export"+c.query, nil)

		got, err := parseFormat(r)
		switch {
		case c.bad && err == nil:
			t.Errorf("parseFormat(%q) accepted an unsupported format", c.query)
		case !c.bad && err != nil:
			t.Errorf("parseFormat(%q) = %v", c.query, err)
		case !c.bad && got != c.want:
			t.Errorf("parseFormat(%q) = %q, want %q", c.query, got, c.want)
		}
	}
}

// maxRows may narrow the cap but never widen it, or it would be a way to ask for
// an unbounded scan.
func Test_exportRowLimit(t *testing.T) {
	cases := []struct {
		query string
		want  int
		bad   bool
	}{
		{"", MaxExportRows, false},
		{"?maxRows=10", 10, false},
		{"?maxRows=999999999", MaxExportRows, false},
		{"?maxRows=0", 0, true},
		{"?maxRows=-5", 0, true},
		{"?maxRows=lots", 0, true},
	}

	for _, c := range cases {
		r := httptest.NewRequest("GET", "/v1/projects/x/logs/export"+c.query, nil)

		got, err := exportRowLimit(r)
		switch {
		case c.bad && err == nil:
			t.Errorf("exportRowLimit(%q) accepted an invalid value", c.query)
		case !c.bad && err != nil:
			t.Errorf("exportRowLimit(%q) = %v", c.query, err)
		case !c.bad && got != c.want:
			t.Errorf("exportRowLimit(%q) = %d, want %d", c.query, got, c.want)
		}
	}
}
