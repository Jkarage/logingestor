package logapp

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// Export bounds. A page is what one query fetches; the row cap is what a single
// request may stream.
//
// The cap exists because an export is one long-running query holding a
// connection, and this service runs a single pool. Anything larger belongs to a
// scheduled job, which does not exist yet — so the limit is stated in a response
// header rather than silently applied.
const (
	exportPageSize = 1000
	MaxExportRows  = 100_000
)

// Export formats.
const (
	formatNDJSON = "ndjson"
	formatCSV    = "csv"
)

// csvHeader is the column order of a CSV export. It is fixed: a spreadsheet
// built against one export must keep working against the next.
var csvHeader = []string{
	"id", "timestamp", "level", "source", "message",
	"projectId", "sourceType", "tags", "meta",
}

// exportProject handles GET /v1/projects/{project_id}/logs/export.
func (a *app) exportProject(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	filter, _, _, errResp := parseLogQuery(r)
	if errResp != nil {
		return errResp
	}
	filter.ProjectID = projectID

	return a.streamExport(ctx, r, filter, "logs-"+projectID.String())
}

// exportOrg handles GET /v1/orgs/{org_id}/logs/export.
func (a *app) exportOrg(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	visible, errResp := a.visibleProjects(ctx, orgID)
	if errResp != nil {
		return errResp
	}

	selected, errResp := selectProjects(r, visible)
	if errResp != nil {
		return errResp
	}

	filter, _, _, errResp := parseLogQuery(r)
	if errResp != nil {
		return errResp
	}

	if len(selected) == 0 {
		// Entitled to ask, nothing to give: an empty export, not a denial.
		return a.streamEmpty(ctx, r, "logs-"+orgID.String())
	}

	filter.ProjectIDs = selected

	return a.streamExport(ctx, r, filter, "logs-"+orgID.String())
}

// parseFormat reads the requested format, defaulting to NDJSON because that is
// what streams without a schema and what every log tool ingests.
func parseFormat(r *http.Request) (string, error) {
	switch f := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))); f {
	case "", formatNDJSON, "jsonl", "json":
		return formatNDJSON, nil
	case formatCSV:
		return formatCSV, nil
	default:
		return "", fmt.Errorf("invalid format %q (want ndjson|csv)", f)
	}
}

// exportRowLimit reads an optional lower cap, so a caller can ask for less than
// the maximum but never more.
func exportRowLimit(r *http.Request) (int, error) {
	v := r.URL.Query().Get("maxRows")
	if v == "" {
		return MaxExportRows, nil
	}

	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid 'maxRows'")
	}

	return min(n, MaxExportRows), nil
}

// streamEmpty writes a well-formed empty export, so a client's parser is never
// handed a zero-length body it has to special-case.
func (a *app) streamEmpty(ctx context.Context, r *http.Request, filename string) web.Encoder {
	format, err := parseFormat(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	w := web.GetWriter(ctx)
	if w == nil {
		return errs.New(errs.Internal, errors.New("no response writer"))
	}

	writeExportHeaders(w, format, filename, MaxExportRows)

	if format == formatCSV {
		cw := csv.NewWriter(w)
		_ = cw.Write(csvHeader)
		cw.Flush()
	}

	return web.NewNoResponse()
}

// streamExport pages through the query and writes rows straight to the client.
//
// The response is streamed rather than assembled: 100k logs is tens of megabytes
// and buffering that per concurrent export is how a single-instance API runs out
// of memory. Streaming means the status line is already sent when a later page
// fails, so a mid-stream error can only be reported by ending the body — which
// is why the row cap and the page size are conservative.
func (a *app) streamExport(ctx context.Context, r *http.Request, filter logbus.QueryFilter, filename string) web.Encoder {
	format, err := parseFormat(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	maxRows, err := exportRowLimit(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	// Counting is pointless work for an export, and on a large project it is the
	// expensive part of the query.
	filter.TotalMode = logbus.TotalNone

	// The first page is fetched before any header is written, so the common
	// failures — a bad filter, a window too wide, a dead database — are still
	// ordinary error responses.
	first, err := a.logBus.Query(ctx, filter, exportPageSize, "")
	if err != nil {
		if errors.Is(err, logbus.ErrWindowTooWide) {
			return errs.New(errs.InvalidArgument, err)
		}
		return errs.Errorf(errs.Internal, "export query: %s", err)
	}

	w := web.GetWriter(ctx)
	if w == nil {
		return errs.New(errs.Internal, errors.New("no response writer"))
	}

	writeExportHeaders(w, format, filename, maxRows)

	fetch := func(cursor string) (logbus.QueryResult, error) {
		if cursor == "" {
			return first, nil
		}
		return a.logBus.Query(ctx, filter, exportPageSize, cursor)
	}

	written, err := writeExport(ctx, w, fetch, format, maxRows)
	if err != nil {
		// The status line is long gone by now, so this can only be reported to the
		// log: either the client hung up or a later page failed.
		a.log.Info(ctx, "export: stopped early", "written", written, "err", err)
	}

	return web.NewNoResponse()
}

// writeExport pages through fetch and writes every row, stopping at maxRows.
//
// It takes the fetch function rather than the query so the paging rules — follow
// the cursor, stop at the cap, stop when the client goes away — are testable
// without a database or an HTTP server. It returns how many rows it wrote, which
// is what the caller logs when something cut the stream short.
func writeExport(ctx context.Context, w io.Writer, fetch func(cursor string) (logbus.QueryResult, error), format string, maxRows int) (int, error) {
	buf := bufio.NewWriterSize(w, 64*1024)

	writeRow, finish := rowWriter(buf, format)

	written := 0
	cursor := ""

	for {
		page, err := fetch(cursor)
		if err != nil {
			finish()
			_ = buf.Flush()
			return written, fmt.Errorf("fetch page: %w", err)
		}

		for _, l := range page.Logs {
			if written >= maxRows {
				finish()
				return written, buf.Flush()
			}

			if err := writeRow(l); err != nil {
				return written, fmt.Errorf("write row: %w", err)
			}
			written++
		}

		if page.NextCursor == nil {
			break
		}

		// Flush per page: an export is watched by a human, and a download that
		// only moves at the end looks like a hang.
		if err := buf.Flush(); err != nil {
			return written, fmt.Errorf("flush: %w", err)
		}

		if err := ctx.Err(); err != nil {
			return written, err
		}

		cursor = *page.NextCursor
	}

	finish()

	return written, buf.Flush()
}

// writeExportHeaders sets the content type, the download filename and the cap
// that was applied. The cap is a header because a streamed body cannot report
// afterwards that it was truncated: a client that receives exactly this many
// rows knows to narrow its filter.
func writeExportHeaders(w http.ResponseWriter, format, filename string, maxRows int) {
	ext := "ndjson"
	contentType := "application/x-ndjson"
	if format == formatCSV {
		ext = "csv"
		contentType = "text/csv; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, filename, ext))
	w.Header().Set("X-Export-Row-Limit", strconv.Itoa(maxRows))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// rowWriter returns a per-log writer for the format, plus the call that closes
// it out.
func rowWriter(buf *bufio.Writer, format string) (func(logbus.Log) error, func()) {
	if format == formatCSV {
		cw := csv.NewWriter(buf)
		_ = cw.Write(csvHeader)

		return func(l logbus.Log) error {
			return cw.Write(csvRow(l))
		}, cw.Flush
	}

	enc := json.NewEncoder(buf)

	return func(l logbus.Log) error {
		// The same entry shape the list endpoint returns, one JSON object per
		// line, so a consumer parses the export with the code it already has.
		return enc.Encode(toAppLogEntry(l))
	}, func() {}
}

// csvRow flattens a log into the fixed column order. Tags and meta keep their
// JSON encoding rather than being spread into columns, because their shape is
// per-tenant and a stable header matters more.
func csvRow(l logbus.Log) []string {
	tags := ""
	if len(l.Tags) > 0 {
		tags = strings.Join(l.Tags, ",")
	}

	meta := ""
	if len(l.Meta) > 0 {
		if b, err := json.Marshal(l.Meta); err == nil {
			meta = string(b)
		}
	}

	return []string{
		l.ID.String(),
		l.Timestamp.UTC().Format(time.RFC3339Nano),
		l.Level.String(),
		l.Source,
		l.Message,
		l.ProjectID.String(),
		l.SourceType,
		tags,
		meta,
	}
}
