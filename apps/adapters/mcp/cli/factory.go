package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	frameworktesting "commander/framework/testing"
	"commander/providers/fs"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
	fsactions "commander/providers/fs/controller/actions"
	fsagents "commander/providers/fs/controller/agents"
	fsexecutions "commander/providers/fs/controller/executions"
	fsmissions "commander/providers/fs/controller/missions"
	fsoperations "commander/providers/fs/controller/operations"
	fsreports "commander/providers/fs/controller/reports"
	fstesting "commander/providers/fs/testing"
)

type dependencies struct {
	Bus            comms.Bus
	ArtifactStore  artifactstore.ArtifactStore
	ExecutionStore executions.ExecutionStore
	AgentStore     agents.AgentStore
	MissionStore   missions.MissionStore
	OperationStore operations.OperationStore
	ActionStore    actions.ActionStore
	ReportStore    reports.ReportStore
	TestCleaner    frameworktesting.TestCleaner
	closers        []io.Closer
}

func (d *dependencies) Close() error {
	var err error
	for i := len(d.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, d.closers[i].Close())
	}
	d.closers = nil
	return err
}

func openDependencies(ctx context.Context, configuration config) (result *dependencies, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deps := &dependencies{}
	defer func() {
		if err != nil {
			err = errors.Join(err, deps.Close())
		}
	}()
	busRoot, err := fs.FilesystemPath(configuration.busURL)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	deps.Bus, err = fscomms.NewFsBus(busRoot)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	deps.closers = append(deps.closers, deps.Bus)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storageRoot, err := fs.FilesystemPath(configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	deps.ArtifactStore, err = fsartifact.NewArtifactFsStore(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("storage-url: open artifact store: %w", err)
	}
	deps.ReportStore, err = fsreports.NewReportFsStore(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("storage-url: open report store: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	databaseRoot, err := fs.FilesystemPath(configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.AgentStore, err = fsagents.NewAgentFsStore(databaseRoot)
	if err != nil {
		return nil, fmt.Errorf("database-url: open agent inventory store: %w", err)
	}
	deps.MissionStore, err = fsmissions.NewMissionFsStore(databaseRoot)
	if err != nil {
		return nil, fmt.Errorf("database-url: open mission store: %w", err)
	}
	deps.OperationStore, err = fsoperations.NewOperationFsStore(databaseRoot)
	if err != nil {
		return nil, fmt.Errorf("database-url: open operation store: %w", err)
	}
	deps.ActionStore, err = fsactions.NewActionFsStore(databaseRoot)
	if err != nil {
		return nil, fmt.Errorf("database-url: open action store: %w", err)
	}
	deps.ExecutionStore, err = fsexecutions.NewExecutionFsStore(databaseRoot)
	if err != nil {
		return nil, fmt.Errorf("database-url: open execution store: %w", err)
	}
	if os.Getenv("ENV") == "tests" {
		cleaner, err := fstesting.NewTestFsCleaner(storageRoot, databaseRoot)
		if err != nil {
			return nil, fmt.Errorf("test cleanup: %w", err)
		}
		deps.TestCleaner = &testCleaner{missions: deps.MissionStore, state: cleaner}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}
