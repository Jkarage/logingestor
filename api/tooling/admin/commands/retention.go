package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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

	// A manual run drains the whole backlog rather than stopping on the budget
	// the scheduled worker uses, so there is no row or time cap here. Deletes are
	// still batched, and cancelling (Ctrl-C) stops cleanly between batches.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg2 := retention.DefaultConfig()
	cfg2.MaxRows = 0
	cfg2.MaxRuntime = 0

	res, err := retention.Run(ctx, log, db, cfg2)
	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	fmt.Printf("retention complete: infra_deleted=%d app_deleted=%d incomplete=%t\n",
		res.InfraDeleted, res.AppDeleted, res.Incomplete)
	if res.Incomplete {
		fmt.Println("stopped early (cancelled); re-run to continue")
	}
	return nil
}
