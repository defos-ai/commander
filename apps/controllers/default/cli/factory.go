package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/providers/fs"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
	fsactions "commander/providers/fs/controller/actions"
	fsagents "commander/providers/fs/controller/agents"
	fsexecutions "commander/providers/fs/controller/executions"
	fsmissions "commander/providers/fs/controller/missions"
	fsoperations "commander/providers/fs/controller/operations"
	fsreports "commander/providers/fs/controller/reports"
	mongoactions "commander/providers/mongo/controller/actions"
	mongoagents "commander/providers/mongo/controller/agents"
	mongoexecutions "commander/providers/mongo/controller/executions"
	mongomissions "commander/providers/mongo/controller/missions"
	mongostore "commander/providers/mongo/store"
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
	mongo          *mongostore.MongoStore
	closers        []func() error
}

func (d *dependencies) Close() error {
	var err error
	for i := len(d.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, d.closers[i]())
	}
	d.closers = nil
	return err
}

func openDependencies(ctx context.Context, configuration config) (result *dependencies, err error) {
	deps := &dependencies{}
	defer func() {
		if err != nil {
			err = errors.Join(err, deps.Close())
		}
	}()
	deps.Bus, err = createBus(ctx, configuration.busURL)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	deps.closers = append(deps.closers, deps.Bus.Close)
	deps.ArtifactStore, err = createArtifactStore(ctx, configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	deps.ReportStore, err = createReportStore(ctx, configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	deps.OperationStore, err = createOperationStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.AgentStore, err = deps.createAgentStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.MissionStore, err = deps.createMissionStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.ActionStore, err = deps.createActionStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.ExecutionStore, err = deps.createExecutionStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}

func createBus(ctx context.Context, rawURL string) (comms.Bus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fscomms.NewFsBus(path)
	case "amqp", "amqps":
		return nil, errors.New("RabbitMQ bus is not implemented")
	default:
		return nil, fmt.Errorf("unsupported bus scheme %q", scheme)
	}
}

func createArtifactStore(ctx context.Context, rawURL string) (artifactstore.ArtifactStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsartifact.NewArtifactFsStore(path)
	case "s3":
		return nil, errors.New("S3 artifact store is not implemented")
	default:
		return nil, fmt.Errorf("unsupported artifact store scheme %q", scheme)
	}
}

func createReportStore(ctx context.Context, rawURL string) (reports.ReportStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsreports.NewReportFsStore(path)
	case "s3":
		return nil, errors.New("S3 report store is not implemented")
	default:
		return nil, fmt.Errorf("unsupported report store scheme %q", scheme)
	}
}

func (d *dependencies) createAgentStore(ctx context.Context, rawURL string) (agents.AgentStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsagents.NewAgentFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongoagents.NewAgentMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported agent store scheme %q", scheme)
	}
}

func createOperationStore(ctx context.Context, rawURL string) (operations.OperationStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsoperations.NewOperationFsStore(path)
	case "mongodb", "mongodb+srv":
		return nil, errors.New("MongoDB operation store is not implemented")
	default:
		return nil, fmt.Errorf("unsupported operation store scheme %q", scheme)
	}
}

func (d *dependencies) createActionStore(ctx context.Context, rawURL string) (actions.ActionStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsactions.NewActionFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongoactions.NewActionMongoStore(d.mongo), nil
	default:
		return nil, fmt.Errorf("unsupported action store scheme %q", scheme)
	}
}

func (d *dependencies) createExecutionStore(ctx context.Context, rawURL string) (executions.ExecutionStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsexecutions.NewExecutionFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongoexecutions.NewExecutionMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported execution store scheme %q", scheme)
	}
}

func (d *dependencies) createMissionStore(ctx context.Context, rawURL string) (missions.MissionStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheme := connectionScheme(rawURL)
	switch scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsmissions.NewMissionFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongomissions.NewMissionMongoStore(d.mongo), nil
	default:
		return nil, fmt.Errorf("unsupported mission store scheme %q", scheme)
	}
}

func (d *dependencies) connectMongo(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.mongo != nil {
		return nil
	}
	database, err := mongostore.Open(ctx, rawURL)
	if err != nil {
		return err
	}
	d.mongo = database
	d.closers = append(d.closers, func() error {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return database.Close(shutdown)
	})
	return nil
}

func connectionScheme(rawURL string) string {
	if filepath.VolumeName(rawURL) != "" {
		return "file"
	}
	// Parse only the prefix: MongoDB seed lists are not standard URL authorities.
	if prefix, _, ok := strings.Cut(rawURL, ":"); ok {
		if parsed, err := url.Parse(prefix + ":"); err == nil && parsed.Scheme != "" {
			return parsed.Scheme
		}
	}
	return "file"
}
