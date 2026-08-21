package clienterrordb

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages client error data access.
type Store struct {
	log *logger.Logger
	db  *sqlx.DB
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{log: log, db: db}
}

const eventColumns = `id, event_id, org_id, project_id, user_id, role, level, kind, name, message,
	stack, component_stack, resolved_stack, release, environment, url, user_agent, api, breadcrumbs,
	occurred_at, received_at, fingerprint, issue_id, sampled_count`

// Ingest stores a batch and returns how many rows were new.
//
// One statement for the whole batch, and the browser is waiting on it. A
// duplicate event id is dropped rather than conflicting, which is what makes a
// sendBeacon retry harmless.
func (s *Store) Ingest(ctx context.Context, events []clienterrorbus.Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	const q = `
	INSERT INTO client_error_events
		(id, event_id, org_id, project_id, user_id, role, level, kind, name, message, stack,
		 component_stack, release, environment, url, user_agent, api, breadcrumbs,
		 occurred_at, received_at, sampled_count)
	VALUES
		(:id, :event_id, :org_id, :project_id, :user_id, :role, :level, :kind, :name, :message, :stack,
		 :component_stack, :release, :environment, :url, :user_agent, :api, :breadcrumbs,
		 :occurred_at, :received_at, :sampled_count)
	ON CONFLICT (event_id) DO NOTHING`

	rows := make([]eventDB, 0, len(events))
	for _, e := range events {
		rows = append(rows, toDBEvent(e))
	}

	res, err := s.db.NamedExecContext(ctx, q, rows)
	if err != nil {
		return 0, fmt.Errorf("namedexeccontext: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		// The insert succeeded; only the count is unavailable. Reporting the
		// batch size is closer to the truth than reporting zero.
		return len(events), nil
	}

	return int(n), nil
}

// ClaimUnprocessed takes a batch of ungrouped events for this worker.
//
// SKIP LOCKED is what allows more than one worker without a lease column: a row
// another worker holds is invisible to this one for the length of its
// transaction. The attempt counter is incremented on claim, so an event that
// kills the worker mid-processing still runs out of attempts instead of being
// retried forever.
func (s *Store) ClaimUnprocessed(ctx context.Context, limit int, maxAttempts int) ([]clienterrorbus.Event, error) {
	q := `
	WITH claimed AS (
		SELECT id FROM client_error_events
		WHERE processed_at IS NULL AND process_attempts < $2
		ORDER BY received_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	)
	UPDATE client_error_events e
	SET process_attempts = e.process_attempts + 1
	FROM claimed
	WHERE e.id = claimed.id
	RETURNING ` + prefixed("e", eventColumns)

	var rows []eventDB
	if err := s.db.SelectContext(ctx, &rows, q, limit, maxAttempts); err != nil {
		return nil, fmt.Errorf("selectcontext: %w", err)
	}

	out := make([]clienterrorbus.Event, len(rows))
	for i, r := range rows {
		out[i] = toBusEvent(r)
	}

	return out, nil
}

// prefixed qualifies a column list with a table alias, which RETURNING needs
// when the statement joins.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}

	return strings.Join(parts, ", ")
}

// AttachToIssue records the event's group and marks it done. The resolved stack
// is stored beside the raw one, which is kept: it is what the browser actually
// sent and the only thing that can be resolved again if a better map arrives.
func (s *Store) AttachToIssue(ctx context.Context, eventID, issueID uuid.UUID, fingerprint string, version int, resolvedStack string) error {
	const q = `
	UPDATE client_error_events
	SET issue_id = $2, fingerprint = $3, fingerprint_version = $4,
	    resolved_stack = NULLIF($5, ''),
	    processed_at = NOW(), process_error = NULL
	WHERE id = $1`

	if _, err := s.db.ExecContext(ctx, q, eventID, issueID, fingerprint, version, resolvedStack); err != nil {
		return fmt.Errorf("execcontext: %w", err)
	}

	return nil
}

// MarkFailed records why an event could not be grouped, and sets it aside once
// it has used up its attempts so it stops being claimed.
func (s *Store) MarkFailed(ctx context.Context, eventID uuid.UUID, reason string, maxAttempts int) error {
	const q = `
	UPDATE client_error_events
	SET process_error = $2,
	    processed_at = CASE WHEN process_attempts >= $3 THEN NOW() ELSE NULL END
	WHERE id = $1`

	if _, err := s.db.ExecContext(ctx, q, eventID, truncate(reason, 500), maxAttempts); err != nil {
		return fmt.Errorf("execcontext: %w", err)
	}

	return nil
}

// UpsertIssue creates the issue for a fingerprint or returns the existing one.
// created reports which happened, because a new issue is worth an alert and an
// existing one is not.
func (s *Store) UpsertIssue(ctx context.Context, i clienterrorbus.Issue) (clienterrorbus.Issue, bool, error) {
	// The unique indexes are partial — one for org-scoped issues, one for the
	// anonymous bucket — so ON CONFLICT cannot name a single constraint. Reading
	// first and inserting on a miss is correct here because the insert is still
	// guarded by those indexes: a lost race surfaces as a duplicate-key error and
	// the retry finds the winner's row.
	existing, err := s.queryIssueByFingerprint(ctx, i.OrgID, i.ProjectID, i.Fingerprint)
	switch {
	case err == nil:
		return existing, false, nil
	case !errors.Is(err, clienterrorbus.ErrNotFound):
		return clienterrorbus.Issue{}, false, err
	}

	const q = `
	INSERT INTO client_error_issues
		(id, org_id, project_id, fingerprint, title, culprit, level, kind, status,
		 event_count, first_seen_at, last_seen_at, sample_event_id)
	VALUES
		($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $10, $11)`

	if _, err := s.db.ExecContext(ctx, q,
		i.ID, i.OrgID, i.ProjectID, i.Fingerprint, i.Title, i.Culprit, i.Level, i.Kind, i.Status,
		i.FirstSeenAt.UTC(), i.SampleEventID); err != nil {

		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			// Another worker created it between the read and the insert.
			existing, rerr := s.queryIssueByFingerprint(ctx, i.OrgID, i.ProjectID, i.Fingerprint)
			if rerr != nil {
				return clienterrorbus.Issue{}, false, rerr
			}
			return existing, false, nil
		}

		return clienterrorbus.Issue{}, false, fmt.Errorf("execcontext: %w", err)
	}

	i.EventCount = 1

	return i, true, nil
}

// queryIssueByFingerprint finds an issue in its scope: the project when there is
// one, otherwise the org, otherwise the anonymous bucket. The three cases match
// the three partial unique indexes exactly, so a lookup and an insert can never
// disagree about which issue a fingerprint belongs to.
func (s *Store) queryIssueByFingerprint(ctx context.Context, orgID, projectID *uuid.UUID, fingerprint string) (clienterrorbus.Issue, error) {
	const base = `SELECT ` + issueColumns + `, 0 AS affected_users, 0 AS affected_orgs, NULL AS releases
	FROM client_error_issues WHERE fingerprint = $1 AND `

	var q string
	args := []any{fingerprint}

	switch {
	case projectID != nil:
		q = base + `project_id = $2`
		args = append(args, *projectID)
	case orgID != nil:
		q = base + `project_id IS NULL AND org_id = $2`
		args = append(args, *orgID)
	default:
		q = base + `project_id IS NULL AND org_id IS NULL`
	}

	var db issueDB
	if err := s.db.GetContext(ctx, &db, q, args...); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return clienterrorbus.Issue{}, clienterrorbus.ErrNotFound
		}
		return clienterrorbus.Issue{}, fmt.Errorf("getcontext: %w", err)
	}

	return toBusIssue(db), nil
}

const issueColumns = `id, org_id, project_id, fingerprint, title, culprit, level, kind, status,
	regressed, event_count, first_seen_at, last_seen_at, resolved_at, assignee_id,
	sample_event_id`

// TouchIssue advances an existing issue for a new event and reports whether the
// event revived a resolved issue.
//
// The whole statement is one round trip because the regression decision has to
// be made from the row as it was: reading the status and then updating it would
// let two workers both decide they were the one that reopened it.
func (s *Store) TouchIssue(ctx context.Context, issueID uuid.UUID, at time.Time, level string, sampleEventID uuid.UUID, count int) (bool, error) {
	const q = `
	UPDATE client_error_issues
	SET event_count = event_count + $4,
	    last_seen_at = GREATEST(last_seen_at, $2),
	    first_seen_at = LEAST(first_seen_at, $2),
	    sample_event_id = $3,

	    -- A resolved issue that fires again is unresolved and flagged; an ignored
	    -- one stays ignored, because ignoring is a standing decision.
	    status = CASE WHEN status = 'resolved' THEN 'unresolved' ELSE status END,
	    regressed = CASE WHEN status = 'resolved' THEN TRUE ELSE regressed END,
	    resolved_at = CASE WHEN status = 'resolved' THEN NULL ELSE resolved_at END,

	    -- The issue carries the worst level it has been seen at, so a warning
	    -- that turns out to also throw is not filed as a warning.
	    level = CASE WHEN $5 = 'fatal' OR (level = 'warning' AND $5 = 'error') THEN $5 ELSE level END
	WHERE id = $1
	RETURNING regressed AND resolved_at IS NULL AS reopened`

	var reopened bool
	if err := s.db.GetContext(ctx, &reopened, q, issueID, at.UTC(), sampleEventID, count, level); err != nil {
		return false, fmt.Errorf("getcontext: %w", err)
	}

	return reopened, nil
}

// RecordFacets adds the distinct users, orgs and releases an issue has been seen
// with. Re-seeing one is a no-op.
func (s *Store) RecordFacets(ctx context.Context, issueID uuid.UUID, facets map[string][]string) error {
	const q = `
	INSERT INTO client_error_issue_facets (issue_id, facet, value)
	VALUES ($1, $2, $3)
	ON CONFLICT (issue_id, facet, value) DO NOTHING`

	for facet, values := range facets {
		for _, v := range values {
			if v == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, q, issueID, facet, truncate(v, 200)); err != nil {
				return fmt.Errorf("execcontext: %w", err)
			}
		}
	}

	return nil
}

// facetSelect counts the distinct facets for each issue in the outer query. It
// is a lateral rather than a join so the counts cannot multiply the row.
const facetSelect = `
	LEFT JOIN LATERAL (
		SELECT
			count(*) FILTER (WHERE f.facet = 'user') AS affected_users,
			count(*) FILTER (WHERE f.facet = 'org')  AS affected_orgs,
			string_agg(f.value, ',') FILTER (WHERE f.facet = 'release') AS releases
		FROM client_error_issue_facets f
		WHERE f.issue_id = i.id
	) facets ON TRUE`

// QueryIssueByID returns one issue with its facet counts.
func (s *Store) QueryIssueByID(ctx context.Context, id uuid.UUID) (clienterrorbus.Issue, error) {
	q := `SELECT ` + prefixed("i", issueColumns) + `,
		COALESCE(facets.affected_users, 0) AS affected_users,
		COALESCE(facets.affected_orgs, 0)  AS affected_orgs,
		facets.releases
	FROM client_error_issues i` + facetSelect + `
	WHERE i.id = $1`

	var db issueDB
	if err := s.db.GetContext(ctx, &db, q, id); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return clienterrorbus.Issue{}, clienterrorbus.ErrNotFound
		}
		return clienterrorbus.Issue{}, fmt.Errorf("getcontext: %w", err)
	}

	return toBusIssue(db), nil
}

// QueryIssues lists issues for the filter and returns the next page cursor.
func (s *Store) QueryIssues(ctx context.Context, f clienterrorbus.IssueFilter) ([]clienterrorbus.Issue, string, error) {
	var (
		where []string
		args  []any
	)

	add := func(clause string, arg any) {
		args = append(args, arg)
		where = append(where, strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args))))
	}

	switch {
	case f.AllOrgs:
		// Every org plus the anonymous bucket. Super admin only, enforced above.
	case f.OrgID != nil:
		add("i.org_id = ?", *f.OrgID)
	default:
		where = append(where, "i.org_id IS NULL")
	}

	if f.ProjectID != nil {
		add("i.project_id = ?", *f.ProjectID)
	}

	if f.Status != "" {
		add("i.status = ?", f.Status)
	}
	if f.Since != nil {
		add("i.last_seen_at >= ?", f.Since.UTC())
	}
	if f.Release != "" {
		add(`EXISTS (SELECT 1 FROM client_error_issue_facets rf
			WHERE rf.issue_id = i.id AND rf.facet = 'release' AND rf.value = ?)`, f.Release)
	}

	// Sorting is by the requested key with the id as a tiebreaker, so the cursor
	// is stable when several issues share a last-seen instant or a count.
	var orderCol string
	switch f.Sort {
	case clienterrorbus.SortCount:
		orderCol = "i.event_count"
	case clienterrorbus.SortUsers:
		orderCol = "COALESCE(facets.affected_users, 0)"
	default:
		orderCol = "i.last_seen_at"
	}

	if f.Cursor != "" {
		value, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		args = append(args, value, id)
		where = append(where, fmt.Sprintf("(%s, i.id) < ($%d, $%d)", orderCol, len(args)-1, len(args)))
	}

	q := `SELECT ` + prefixed("i", issueColumns) + `,
		COALESCE(facets.affected_users, 0) AS affected_users,
		COALESCE(facets.affected_orgs, 0)  AS affected_orgs,
		facets.releases
	FROM client_error_issues i` + facetSelect

	if len(where) > 0 {
		q += "\n\tWHERE " + strings.Join(where, "\n\t  AND ")
	}

	args = append(args, f.Limit)
	q += fmt.Sprintf("\n\tORDER BY %s DESC, i.id DESC\n\tLIMIT $%d", orderCol, len(args))

	var rows []issueDB
	if err := s.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, "", fmt.Errorf("selectcontext: %w", err)
	}

	out := make([]clienterrorbus.Issue, len(rows))
	for i, r := range rows {
		out[i] = toBusIssue(r)
	}

	var next string
	if len(rows) == f.Limit {
		last := rows[len(rows)-1]
		switch f.Sort {
		case clienterrorbus.SortCount:
			next = encodeCursor(strconv.FormatInt(last.EventCount, 10), last.ID)
		case clienterrorbus.SortUsers:
			next = encodeCursor(strconv.Itoa(last.AffectedUsers), last.ID)
		default:
			next = encodeCursor(last.LastSeenAt.UTC().Format(time.RFC3339Nano), last.ID)
		}
	}

	return out, next, nil
}

// UpdateIssue writes a triage decision.
func (s *Store) UpdateIssue(ctx context.Context, i clienterrorbus.Issue) error {
	const q = `
	UPDATE client_error_issues
	SET status = $2, regressed = $3, resolved_at = $4, assignee_id = $5
	WHERE id = $1`

	if _, err := s.db.ExecContext(ctx, q, i.ID, i.Status, i.Regressed, i.ResolvedAt, i.AssigneeID); err != nil {
		return fmt.Errorf("execcontext: %w", err)
	}

	return nil
}

// QueryIssueEvents returns an issue's most recent events.
func (s *Store) QueryIssueEvents(ctx context.Context, issueID uuid.UUID, limit int) ([]clienterrorbus.Event, error) {
	q := `SELECT ` + eventColumns + `
	FROM client_error_events
	WHERE issue_id = $1
	ORDER BY occurred_at DESC
	LIMIT $2`

	var rows []eventDB
	if err := s.db.SelectContext(ctx, &rows, q, issueID, limit); err != nil {
		return nil, fmt.Errorf("selectcontext: %w", err)
	}

	out := make([]clienterrorbus.Event, len(rows))
	for i, r := range rows {
		out[i] = toBusEvent(r)
	}

	return out, nil
}

// QueryIssueSeries returns the issue's event count per bucket, including empty
// buckets so a sparkline has a point for every interval.
func (s *Store) QueryIssueSeries(ctx context.Context, issueID uuid.UUID, from, to time.Time, interval time.Duration) ([]clienterrorbus.Bucket, error) {
	const q = `
	SELECT g.bucket AS ts, COALESCE(sum(e.sampled_count), 0) AS count
	FROM generate_series($2::timestamptz, $3::timestamptz, $4::interval) AS g(bucket)
	LEFT JOIN client_error_events e
		ON e.issue_id = $1
		AND e.occurred_at >= g.bucket
		AND e.occurred_at < g.bucket + $4::interval
	GROUP BY g.bucket
	ORDER BY g.bucket`

	type row struct {
		TS    time.Time `db:"ts"`
		Count int64     `db:"count"`
	}

	// The series is generated from truncated bounds so buckets line up with the
	// clock rather than with the request time.
	step := fmt.Sprintf("%d seconds", int(interval.Seconds()))
	start := from.UTC().Truncate(interval)
	end := to.UTC().Truncate(interval)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q, issueID, start, end, step); err != nil {
		return nil, fmt.Errorf("selectcontext: %w", err)
	}

	out := make([]clienterrorbus.Bucket, len(rows))
	for i, r := range rows {
		out[i] = clienterrorbus.Bucket{TS: r.TS.UTC(), Count: r.Count}
	}

	return out, nil
}

// QueryStats returns the dashboard tiles for a window.
func (s *Store) QueryStats(ctx context.Context, orgID, projectID *uuid.UUID, allOrgs bool, from, to time.Time) (clienterrorbus.Stats, error) {
	scope := "org_id IS NULL"
	args := []any{from.UTC(), to.UTC()}

	switch {
	case allOrgs:
		scope = "TRUE"
	case orgID != nil:
		scope = "org_id = $3"
		args = append(args, *orgID)
	}

	if projectID != nil {
		scope += fmt.Sprintf(" AND project_id = $%d", len(args)+1)
		args = append(args, *projectID)
	}

	q := `
	SELECT
		(SELECT COALESCE(sum(sampled_count), 0) FROM client_error_events
		  WHERE ` + scope + ` AND occurred_at >= $1 AND occurred_at < $2) AS events,
		(SELECT count(1) FROM client_error_issues
		  WHERE ` + scope + ` AND last_seen_at >= $1) AS issues,
		(SELECT count(1) FROM client_error_issues
		  WHERE ` + scope + ` AND first_seen_at >= $1) AS new_issues,
		(SELECT count(1) FROM client_error_issues
		  WHERE ` + scope + ` AND status = 'unresolved') AS unresolved,
		(SELECT count(DISTINCT user_id) FROM client_error_events
		  WHERE ` + scope + ` AND user_id IS NOT NULL AND occurred_at >= $1 AND occurred_at < $2) AS affected_users`

	var row struct {
		Events        int64 `db:"events"`
		Issues        int64 `db:"issues"`
		NewIssues     int64 `db:"new_issues"`
		Unresolved    int64 `db:"unresolved"`
		AffectedUsers int   `db:"affected_users"`
	}
	if err := s.db.GetContext(ctx, &row, q, args...); err != nil {
		return clienterrorbus.Stats{}, fmt.Errorf("getcontext: %w", err)
	}

	return clienterrorbus.Stats{
		From: from.UTC(), To: to.UTC(),
		Events: row.Events, Issues: row.Issues, NewIssues: row.NewIssues,
		Unresolved: row.Unresolved, AffectedUsers: row.AffectedUsers,
	}, nil
}

// PurgeOrg deletes an org's client error records for a deletion request. Issues
// cascade to their facets, and events are removed explicitly because they are
// what actually holds the reported content.
func (s *Store) PurgeOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begintxx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var total int64

	for _, q := range []string{
		`DELETE FROM client_error_events WHERE org_id = $1`,
		`DELETE FROM client_error_issues WHERE org_id = $1`,
	} {
		res, err := tx.ExecContext(ctx, q, orgID)
		if err != nil {
			return 0, fmt.Errorf("execcontext: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return total, nil
}

// encodeCursor packs a sort value and an id into an opaque page token.
func encodeCursor(value string, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value + "|" + id.String()))
}

// decodeCursor unpacks a page token.
func decodeCursor(cursor string) (string, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", uuid.Nil, errors.New("malformed cursor")
	}

	parts := bytes.SplitN(raw, []byte("|"), 2)
	if len(parts) != 2 {
		return "", uuid.Nil, errors.New("malformed cursor")
	}

	id, err := uuid.Parse(string(parts[1]))
	if err != nil {
		return "", uuid.Nil, errors.New("malformed cursor")
	}

	return string(parts[0]), id, nil
}

// truncate bounds a stored string. Reasons and facet values come from upstream
// code rather than a browser, so a byte cut is fine here.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}

// UpsertArtifact stores a source map, replacing any previous upload of the same
// file for the same release so a re-run of a deploy job is idempotent.
func (s *Store) UpsertArtifact(ctx context.Context, a clienterrorbus.Artifact, content []byte) error {
	const q = `
	INSERT INTO client_error_artifacts
		(id, release, file_name, content, byte_size, compressed, uploaded_by)
	VALUES ($1, $2, $3, $4, $5, TRUE, $6)
	ON CONFLICT (release, file_name) DO UPDATE SET
		content = EXCLUDED.content,
		byte_size = EXCLUDED.byte_size,
		uploaded_by = EXCLUDED.uploaded_by,
		date_created = NOW()`

	if _, err := s.db.ExecContext(ctx, q, a.ID, a.Release, a.FileName, content, a.ByteSize, a.UploadedBy); err != nil {
		return fmt.Errorf("execcontext: %w", err)
	}

	return nil
}

// QueryArtifact returns one stored map, still compressed. A miss is an empty
// slice rather than an error: most frames are vendor chunks with no map, and
// that is not a failure.
func (s *Store) QueryArtifact(ctx context.Context, release, fileName string) ([]byte, error) {
	const q = `SELECT content FROM client_error_artifacts WHERE release = $1 AND file_name = $2`

	var content []byte
	if err := s.db.GetContext(ctx, &content, q, release, fileName); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("getcontext: %w", err)
	}

	return content, nil
}

// QueryArtifactsByRelease lists what has been uploaded for a release, without
// the content.
func (s *Store) QueryArtifactsByRelease(ctx context.Context, release string) ([]clienterrorbus.Artifact, error) {
	const q = `
	SELECT id, release, file_name, byte_size, uploaded_by, date_created
	FROM client_error_artifacts
	WHERE release = $1
	ORDER BY file_name`

	var rows []struct {
		ID          uuid.UUID `db:"id"`
		Release     string    `db:"release"`
		FileName    string    `db:"file_name"`
		ByteSize    int       `db:"byte_size"`
		UploadedBy  string    `db:"uploaded_by"`
		DateCreated time.Time `db:"date_created"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, release); err != nil {
		return nil, fmt.Errorf("selectcontext: %w", err)
	}

	out := make([]clienterrorbus.Artifact, len(rows))
	for i, r := range rows {
		out[i] = clienterrorbus.Artifact{
			ID: r.ID, Release: r.Release, FileName: r.FileName,
			ByteSize: r.ByteSize, UploadedBy: r.UploadedBy,
			DateCreated: r.DateCreated.In(time.Local),
		}
	}

	return out, nil
}

// MarkReleaseForRegroup queues a release's already-grouped events to be grouped
// again, because a map arrived after they were filed.
//
// issue_id is deliberately left in place: the grouping pass reads it to know
// this is a re-group rather than a first sighting, which is what keeps a map
// upload from paging the team about every issue it re-keys.
func (s *Store) MarkReleaseForRegroup(ctx context.Context, release string, belowVersion int) (int64, error) {
	const q = `
	UPDATE client_error_events
	SET processed_at = NULL, process_attempts = 0, process_error = NULL
	WHERE release = $1 AND fingerprint_version < $2 AND processed_at IS NOT NULL`

	res, err := s.db.ExecContext(ctx, q, release, belowVersion)
	if err != nil {
		return 0, fmt.Errorf("execcontext: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}

	return n, nil
}

// DetachFromIssue takes an event's weight off an issue it has moved away from,
// deleting the issue when nothing is left on it.
func (s *Store) DetachFromIssue(ctx context.Context, issueID uuid.UUID, count int) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begintxx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const decrement = `
	UPDATE client_error_issues
	SET event_count = GREATEST(event_count - $2, 0)
	WHERE id = $1`

	if _, err := tx.ExecContext(ctx, decrement, issueID, count); err != nil {
		return fmt.Errorf("decrement: %w", err)
	}

	// An issue with no events left is not history, it is an artefact of the
	// re-keying. Its facets go with it by cascade.
	const cleanup = `
	DELETE FROM client_error_issues i
	WHERE i.id = $1
	  AND NOT EXISTS (SELECT 1 FROM client_error_events e WHERE e.issue_id = i.id)`

	if _, err := tx.ExecContext(ctx, cleanup, issueID); err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
