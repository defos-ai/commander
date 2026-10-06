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
	"commander/framework/domain/controllers"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	frameworktesting "commander/framework/testing"
	"commander/providers/fs"
	fsactions "commander/providers/fs/actions"
	fsagents "commander/providers/fs/agents"
	fsartifacts "commander/providers/fs/artifacts"
	fscomms "commander/providers/fs/comms"
	fscontrollers "commander/providers/fs/controllers"
	fsmissions "commander/providers/fs/missions"
	fsoperations "commander/providers/fs/operations"
	fsreports "commander/providers/fs/reports"
	fstesting "commander/providers/fs/testing"
	mongoactions "commander/providers/mongo/actions"
	mongoagents "commander/providers/mongo/agents"
	mongoartifacts "commander/providers/mongo/artifacts"
	mongocontrollers "commander/providers/mongo/controllers"
	mongomissions "commander/providers/mongo/missions"
	mongooperations "commander/providers/mongo/operations"
	mongostore "commander/providers/mongo/store"
	mongotesting "commander/providers/mongo/testing"
	rabbitmqcomms "commander/providers/rabbitmq/comms"
	s3artifacts "commander/providers/s3/artifacts"
	s3reports "commander/providers/s3/reports"
	s3store "commander/providers/s3/store"
	s3testing "commander/providers/s3/testing"
)

type dependencies struct {
	Bus               comms.Bus
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	AgentStore        agents.AgentStore
	ControllerStore   controllers.ControllerStore
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
	deps.ControllerStore, err = deps.createControllerStore(ctx, configuration.databaseURL)
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
		deps.TestCleaner, err = deps.createTestCleaner(ctx, configuration.storageURL, configuration.databaseURL)
		if err != nil {
			return nil, fmt.Errorf("test cleanup: %w", err)
		}
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
		return rabbitmqcomms.NewRabbitMQBus(ctx, rawURL, rabbitmqcomms.Options{})
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

func (d *dependencies) createControllerStore(ctx context.Context, rawURL string) (controllers.ControllerStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fscontrollers.NewControllerFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongocontrollers.NewControllerMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported controller store scheme %q", scheme)
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
		return &mongoConnectionError{cause: err}
	}
	d.mongo = database
	d.closers = append(d.closers, func() error {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return database.Close(shutdown)
	})
	return nil
}

type mongoConnectionError struct {
	cause error
}

func (e *mongoConnectionError) Error() string {
	return "MongoDB initialization failed; check DATABASE_URL and database availability"
}

func (e *mongoConnectionError) Unwrap() error { return e.cause }

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

func (d *dependencies) createTestCleaner(ctx context.Context, storageURL, databaseURL string) (frameworktesting.TestCleaner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var database, storage frameworktesting.TestCleaner
	switch scheme := connectionScheme(databaseURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(databaseURL)
		if err != nil {
			return nil, err
		}
		database, err = fstesting.NewTestFsDatabaseCleaner(path)
		if err != nil {
			return nil, err
		}
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, databaseURL); err != nil {
			return nil, err
		}
		var err error
		database, err = mongotesting.NewTestMongoCleaner(d.mongo)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported test database scheme %q", scheme)
	}
	switch scheme := connectionScheme(storageURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(storageURL)
		if err != nil {
			return nil, err
		}
		storage, err = fstesting.NewTestFsStorageCleaner(path)
		if err != nil {
			return nil, err
		}
	case "s3":
		if err := d.connectS3(ctx, storageURL); err != nil {
			return nil, err
		}
		var err error
		storage, err = s3testing.NewTestS3Cleaner(d.s3)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported test storage scheme %q", scheme)
	}
	return frameworktesting.NewStateCleaner(d.OperationStore, d.AgentStore, database, storage)
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
		return fsartifacts.NewArtifactFileFsStore(path)
	case "s3":
		if err := d.connectS3(ctx, rawURL); err != nil {
			return nil, err
		}
		return s3artifacts.NewArtifactFileS3Store(d.s3)
	default:
		return nil, fmt.Errorf("unsupported artifact file store scheme %q", scheme)
	}
}
