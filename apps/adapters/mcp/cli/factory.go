package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"commander/framework/comms"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	frameworktesting "commander/framework/testing"
	"commander/providers/fs"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
	fsactions "commander/providers/fs/controller/actions"
	fsagents "commander/providers/fs/controller/agents"
	fsartifacts "commander/providers/fs/controller/artifacts"
	fsmissions "commander/providers/fs/controller/missions"
	fsoperations "commander/providers/fs/controller/operations"
	fsreports "commander/providers/fs/controller/reports"
	fstesting "commander/providers/fs/testing"
	mongoactions "commander/providers/mongo/controller/actions"
	mongoagents "commander/providers/mongo/controller/agents"
	mongoartifacts "commander/providers/mongo/controller/artifacts"
	mongomissions "commander/providers/mongo/controller/missions"
	mongooperations "commander/providers/mongo/controller/operations"
	mongostore "commander/providers/mongo/store"
	s3artifact "commander/providers/s3/artifact"
	s3reports "commander/providers/s3/controller/reports"
	s3store "commander/providers/s3/store"
)

type dependencies struct {
	Bus               comms.Bus
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	AgentStore        agents.AgentStore
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	ActionStore       actions.ActionStore
	ReportStore       reports.ReportStore
	TestCleaner       frameworktesting.TestCleaner
	mongo             *mongostore.MongoStore
	s3                *s3store.S3Store
	closers           []func() error
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
	deps.ArtifactStore, err = deps.createArtifactStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.ArtifactFileStore, err = deps.createArtifactFileStore(ctx, configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	deps.ReportStore, err = deps.createReportStore(ctx, configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	deps.OperationStore, err = deps.createOperationStore(ctx, configuration.databaseURL)
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
	if os.Getenv("ENV") == "tests" {
		cleaner, err := createTestCleaner(ctx, configuration.storageURL, configuration.databaseURL)
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

func (d *dependencies) createArtifactStore(ctx context.Context, rawURL string) (artifacts.ArtifactStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsartifacts.NewArtifactFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongoartifacts.NewArtifactMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported artifact store scheme %q", scheme)
	}
}

func (d *dependencies) createReportStore(ctx context.Context, rawURL string) (reports.ReportStore, error) {
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
		if err := d.connectS3(ctx, rawURL); err != nil {
			return nil, err
		}
		return s3reports.NewReportS3Store(d.s3)
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

func (d *dependencies) createOperationStore(ctx context.Context, rawURL string) (operations.OperationStore, error) {
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
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongooperations.NewOperationMongoStore(ctx, d.mongo)
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

func (d *dependencies) connectS3(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.s3 != nil {
		return nil
	}
	storage, err := s3store.Open(ctx, rawURL)
	if err != nil {
		return err
	}
	d.s3 = storage
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

func createTestCleaner(ctx context.Context, storageURL, databaseURL string) (frameworktesting.TestCleaner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(storageURL); scheme {
	case "file", "fs":
	case "s3":
		return nil, errors.New("S3 test cleanup is not implemented")
	default:
		return nil, fmt.Errorf("unsupported test storage scheme %q", scheme)
	}
	switch scheme := connectionScheme(databaseURL); scheme {
	case "file", "fs":
	case "mongodb", "mongodb+srv":
		return nil, errors.New("MongoDB test cleanup is not implemented")
	default:
		return nil, fmt.Errorf("unsupported test database scheme %q", scheme)
	}
	storagePath, err := fs.FilesystemPath(storageURL)
	if err != nil {
		return nil, err
	}
	databasePath, err := fs.FilesystemPath(databaseURL)
	if err != nil {
		return nil, err
	}
	return fstesting.NewTestFsCleaner(storagePath, databasePath)
}

func (d *dependencies) createArtifactFileStore(ctx context.Context, rawURL string) (artifacts.ArtifactFileStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsartifact.NewArtifactFileFsStore(path)
	case "s3":
		if err := d.connectS3(ctx, rawURL); err != nil {
			return nil, err
		}
		return s3artifact.NewArtifactFileS3Store(d.s3)
	default:
		return nil, fmt.Errorf("unsupported artifact file store scheme %q", scheme)
	}
}
