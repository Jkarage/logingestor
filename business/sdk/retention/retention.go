// Package retention deletes aged log rows. It is source_type-aware: infra logs
// expire on the org plan's infra_retention_days, while app logs follow each
// project's own retention_days — so changing one never affects the other.
package retention

import (
	"context"
	"fmt"

	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Result reports how many rows each pass deleted.
type Result struct {
	InfraDeleted int64
	AppDeleted   int64
}

// Run executes both retention passes and returns the row counts deleted.
func Run(ctx context.Context, log *logger.Logger, db *sqlx.DB) (Result, error) {
	var res Result

	// Infra logs: expire on the org's active-plan infra_retention_days. A value
	// of -1 (or missing) means keep forever and is skipped.
	const infraQ = `
	DELETE FROM logs l
	USING projects p
	JOIN subscriptions s ON s.org_id = p.org_id
	JOIN plans pl        ON pl.id = s.plan_id
	WHERE l.project_id = p.id
	  AND l.source_type = 'infra'
	  AND COALESCE((pl.features->>'infra_retention_days')::int, -1) >= 0
	  AND l.ts < now() - (COALESCE((pl.features->>'infra_retention_days')::int, 0) * interval '1 day')`

	n, err := exec(ctx, db, infraQ)
	if err != nil {
		return res, fmt.Errorf("infra retention: %w", err)
	}
	res.InfraDeleted = n

	// App logs: expire on each project's own retention_days when set.
	const appQ = `
	DELETE FROM logs l
	USING projects p
	WHERE l.project_id = p.id
	  AND l.source_type = 'app'
	  AND p.retention_days IS NOT NULL
	  AND l.ts < now() - (p.retention_days * interval '1 day')`

	n, err = exec(ctx, db, appQ)
	if err != nil {
		return res, fmt.Errorf("app retention: %w", err)
	}
	res.AppDeleted = n

	log.Info(ctx, "retention complete", "infra_deleted", res.InfraDeleted, "app_deleted", res.AppDeleted)
	return res, nil
}

func exec(ctx context.Context, db *sqlx.DB, query string) (int64, error) {
	if err := sqldb.StatusCheck(ctx, db); err != nil {
		return 0, fmt.Errorf("status check: %w", err)
	}
	r, err := db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
