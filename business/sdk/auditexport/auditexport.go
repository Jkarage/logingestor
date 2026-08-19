// Package auditexport ships audit records to an external SIEM over HTTP.
//
// Records are read in keyset order from a persisted cursor and POSTed as
// newline-delimited JSON, which is what Splunk HEC, Datadog, Elastic and most
// generic SIEM ingest endpoints accept. Delivery is at-least-once: the cursor
// advances only after the destination accepts a batch, so a crash mid-flight
// re-sends the batch rather than dropping it. Consumers should therefore treat
// the record id as an idempotency key.
package auditexport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Config describes the destination and batching behaviour.
type Config struct {
	// URL is the SIEM ingest endpoint. Empty disables export entirely.
	URL string

	// Token, when set, is sent as "Authorization: Bearer <token>".
	Token string

	// BatchSize bounds how many records are sent per request.
	BatchSize int

	// Timeout bounds a single delivery attempt.
	Timeout time.Duration
}

// Enabled reports whether export is configured.
func (c Config) Enabled() bool { return c.URL != "" }

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 500
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	return c
}

// Exporter ships audit records to a configured destination.
type Exporter struct {
	log  *logger.Logger
	db   *sqlx.DB
	cfg  Config
	http *http.Client
}

// New constructs an exporter.
func New(log *logger.Logger, db *sqlx.DB, cfg Config) *Exporter {
	cfg = cfg.withDefaults()

	return &Exporter{
		log:  log,
		db:   db,
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// record is one exported audit entry. Field names are stable: a SIEM's parsing
// rules are built against them.
type record struct {
	ID             string          `json:"id"`
	Timestamp      time.Time       `json:"timestamp"`
	OrgID          string          `json:"orgId"`
	ActorID        string          `json:"actorId"`
	ActorName      string          `json:"actorName"`
	ActorIP        string          `json:"actorIp,omitempty"`
	ActorUserAgent string          `json:"actorUserAgent,omitempty"`
	Action         string          `json:"action"`
	TargetID       string          `json:"targetId"`
	TargetType     string          `json:"targetType"`
	TargetName     string          `json:"targetName"`
	Message        string          `json:"message,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
}

type cursor struct {
	LastTimestamp time.Time `db:"last_timestamp"`
	LastID        uuid.UUID `db:"last_id"`
}

// Run exports everything currently outstanding, one batch at a time, and returns
// how many records were delivered.
func (e *Exporter) Run(ctx context.Context) (int, error) {
	if !e.cfg.Enabled() {
		return 0, nil
	}

	var total int

	for {
		if err := ctx.Err(); err != nil {
			return total, nil
		}

		n, err := e.runBatch(ctx)
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}

		total += n
	}

	if total > 0 {
		e.log.Info(ctx, "audit export complete", "delivered", total)
	}

	return total, nil
}

func (e *Exporter) runBatch(ctx context.Context) (int, error) {
	var cur cursor
	const cursorQ = `SELECT last_timestamp, last_id FROM audit_export_cursor WHERE singleton`
	if err := e.db.GetContext(ctx, &cur, cursorQ); err != nil {
		return 0, fmt.Errorf("read cursor: %w", err)
	}

	// Keyset pagination on (timestamp, id): stable under concurrent inserts and
	// never re-reads a delivered row, unlike an OFFSET scan.
	const rowsQ = `
	SELECT
		a.id, a.timestamp, a.org_id, a.actor_id, COALESCE(u.name, '') AS actor_name,
		a.actor_ip, a.actor_user_agent, a.action, a.obj_id, a.obj_domain, a.obj_name,
		COALESCE(a.message, '') AS message, a.data
	FROM audit a
	LEFT JOIN users u ON u.id = a.actor_id
	WHERE (a.timestamp, a.id) > ($1, $2)
	ORDER BY a.timestamp, a.id
	LIMIT $3`

	rows, err := e.db.QueryxContext(ctx, rowsQ, cur.LastTimestamp, cur.LastID, e.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("read audit: %w", err)
	}
	defer rows.Close()

	var (
		batch  []record
		lastTS time.Time
		lastID uuid.UUID
	)

	for rows.Next() {
		var r struct {
			ID        uuid.UUID       `db:"id"`
			Timestamp time.Time       `db:"timestamp"`
			OrgID     uuid.UUID       `db:"org_id"`
			ActorID   uuid.UUID       `db:"actor_id"`
			ActorName string          `db:"actor_name"`
			ActorIP   *string         `db:"actor_ip"`
			ActorUA   *string         `db:"actor_user_agent"`
			Action    string          `db:"action"`
			ObjID     uuid.UUID       `db:"obj_id"`
			ObjDomain string          `db:"obj_domain"`
			ObjName   string          `db:"obj_name"`
			Message   string          `db:"message"`
			Data      json.RawMessage `db:"data"`
		}
		if err := rows.StructScan(&r); err != nil {
			return 0, fmt.Errorf("scan audit: %w", err)
		}

		rec := record{
			ID:         r.ID.String(),
			Timestamp:  r.Timestamp.UTC(),
			OrgID:      r.OrgID.String(),
			ActorID:    r.ActorID.String(),
			ActorName:  r.ActorName,
			Action:     r.Action,
			TargetID:   r.ObjID.String(),
			TargetType: r.ObjDomain,
			TargetName: r.ObjName,
			Message:    r.Message,
			Data:       r.Data,
		}
		if r.ActorIP != nil {
			rec.ActorIP = *r.ActorIP
		}
		if r.ActorUA != nil {
			rec.ActorUserAgent = *r.ActorUA
		}

		batch = append(batch, rec)
		lastTS, lastID = r.Timestamp, r.ID
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate audit: %w", err)
	}

	if len(batch) == 0 {
		return 0, nil
	}

	if err := e.deliver(ctx, batch); err != nil {
		return 0, err
	}

	// Only now is it safe to advance: a failure above leaves the cursor put and
	// the batch is retried on the next run.
	const advanceQ = `
	UPDATE audit_export_cursor
	SET last_timestamp = $1, last_id = $2, date_updated = NOW()
	WHERE singleton`

	if _, err := e.db.ExecContext(ctx, advanceQ, lastTS, lastID); err != nil {
		return 0, fmt.Errorf("advance cursor: %w", err)
	}

	return len(batch), nil
}

// deliver POSTs one batch as newline-delimited JSON.
func (e *Exporter) deliver(ctx context.Context, batch []record) error {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	for _, r := range batch {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.URL, &buf)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	if e.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
	}

	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("deliver audit batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The body may echo the payload; report only the status.
		return fmt.Errorf("audit export destination returned %d", resp.StatusCode)
	}

	return nil
}
