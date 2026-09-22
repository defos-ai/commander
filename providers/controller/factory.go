// Package controller opens persistence used by the controller and mission runner.
package controller

import (
	"context"
	"fmt"

	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	fsactions "commander/providers/fs/controller/actions"
	fsagents "commander/providers/fs/controller/agents"
	fsexecutions "commander/providers/fs/controller/executions"
	fsmissions "commander/providers/fs/controller/missions"
	fsoperations "commander/providers/fs/controller/operations"
	fsreports "commander/providers/fs/controller/reports"
	"commander/providers/internal/connections"
)

// ReportStorage supplies report persistence selected by STORAGE_URL. It is
// opened separately from artifacts so agents need no controller dependencies.
type ReportStorage struct {
	ReportStore reports.ReportStore
}

// Close releases report storage resources after consumers have stopped.
// Filesystem stores hold no persistent connections and need no cleanup.
func (s *ReportStorage) Close() error { return nil }

// OpenReportStorage opens report storage selected by its URL.
func OpenReportStorage(ctx context.Context, rawURL string) (*ReportStorage, error) {
	root, err := filesystemRoot(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	reportStore, err := fsreports.NewReportFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open report store: %w", err)
	}
	return &ReportStorage{ReportStore: reportStore}, nil
}

// Database supplies the domain stores sharing DATABASE_URL.
type Database struct {
	ExecutionStore executions.ExecutionStore
	AgentStore     agentmodel.AgentStore
	MissionStore   missions.MissionStore
	OperationStore operations.OperationStore
	ActionStore    actionmodel.ActionStore
}

// Close releases database resources after consumers have stopped. Filesystem
// stores open and close files for each operation, so there is no shared handle.
func (d *Database) Close() error { return nil }

// OpenDatabase opens the controller's database stores selected by its URL.
func OpenDatabase(ctx context.Context, rawURL string) (*Database, error) {
	root, err := filesystemRoot(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	agentInventory, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open agent inventory store: %w", err)
	}
	missionStore, err := fsmissions.NewMissionFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open mission store: %w", err)
	}
	operationStore, err := fsoperations.NewOperationFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open operation store: %w", err)
	}
	actionStore, err := fsactions.NewActionFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open action store: %w", err)
	}
	executionStore, err := fsexecutions.NewExecutionFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open execution store: %w", err)
	}
	return &Database{
		ExecutionStore: executionStore,
		AgentStore:     agentInventory,
		MissionStore:   missionStore,
		OperationStore: operationStore,
		ActionStore:    actionStore,
	}, nil
}

func filesystemRoot(ctx context.Context, rawURL string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return connections.FilesystemPath(rawURL)
}
