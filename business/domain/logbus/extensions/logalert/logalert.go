// Package logalert provides a logbus extension that fires integration alerts
// after logs are persisted.
package logalert

import (
	"context"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
)

// Extension wraps logbus with alert-firing logic.
type Extension struct {
	bus        logbus.ExtBusiness
	log        *logger.Logger
	projectBus projectbus.ExtBusiness
	alertBus   *integrationbus.Business
}

// NewExtension returns a logbus.Extension that fires integration alerts after each BulkCreate.
func NewExtension(log *logger.Logger, projectBus projectbus.ExtBusiness, alertBus *integrationbus.Business) logbus.Extension {
	return func(bus logbus.ExtBusiness) logbus.ExtBusiness {
		return &Extension{
			bus:        bus,
			log:        log,
			projectBus: projectBus,
			alertBus:   alertBus,
		}
	}
}

// BulkCreate persists logs then fires alerts asynchronously.
func (ext *Extension) BulkCreate(ctx context.Context, entries []logbus.NewLog) ([]logbus.Log, error) {
	logs, err := ext.bus.BulkCreate(ctx, entries)
	if err != nil {
		return nil, err
	}

	if len(logs) > 0 {
		go ext.dispatch(logs)
	}

	return logs, nil
}

// dispatch runs in a goroutine — resolves org IDs and calls FireAlerts for each log.
func (ext *Extension) dispatch(logs []logbus.Log) {
	ctx := context.Background()

	// Resolve projectID → orgID + name once per unique project.
	type projectInfo struct {
		orgID uuid.UUID
		name  string
	}
	cache := make(map[uuid.UUID]projectInfo)

	for _, l := range logs {
		info, ok := cache[l.ProjectID]
		if !ok {
			project, err := ext.projectBus.QueryByID(ctx, l.ProjectID)
			if err != nil {
				ext.log.Error(ctx, "logalert: lookup project", "projectID", l.ProjectID, "err", err)
				continue
			}
			info = projectInfo{orgID: project.OrgID, name: project.Name}
			cache[l.ProjectID] = info
		}

		payload := integrationbus.AlertPayload{
			ProjectName: info.name,
			Level:       l.Level.String(),
			Message:     l.Message,
			Source:      l.Source,
			LogID:       l.ID.String(),
			Timestamp:   l.Timestamp,
		}

		if err := ext.alertBus.FireAlerts(ctx, info.orgID, &l.ProjectID, payload); err != nil {
			ext.log.Error(ctx, "logalert: fire alerts", "logID", l.ID, "err", err)
		}
	}
}

func (ext *Extension) Query(ctx context.Context, filter logbus.QueryFilter, limit int, cursor string) (logbus.QueryResult, error) {
	return ext.bus.Query(ctx, filter, limit, cursor)
}

func (ext *Extension) Stats(ctx context.Context, projectID uuid.UUID) (map[string]int, error) {
	return ext.bus.Stats(ctx, projectID)
}
