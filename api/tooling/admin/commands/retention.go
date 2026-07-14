package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/jkarage/logingestor/business/sdk/retention"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Retention deletes aged log rows (infra by plan, app by project) and reports
// the counts. Intended to be run on a schedule (cron / k8s CronJob).
func Retention(log *logger.Logger, cfg sqldb.Config) error {
	db, err := sqldb.Open(cfg)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := retention.Run(ctx, log, db)
	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	fmt.Printf("retention complete: infra_deleted=%d app_deleted=%d\n", res.InfraDeleted, res.AppDeleted)
	return nil
}
