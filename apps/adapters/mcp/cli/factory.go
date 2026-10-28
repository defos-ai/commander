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

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/domain/sessions"
	frameworktesting "github.com/defos-ai/commander/framework/testing"
	"github.com/defos-ai/commander/providers/fs"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	fssessions "github.com/defos-ai/commander/providers/fs/sessions"
	fstesting "github.com/defos-ai/commander/providers/fs/testing"
	mongoactions "github.com/defos-ai/commander/providers/mongo/actions"
	mongoagents "github.com/defos-ai/commander/providers/mongo/agents"
	mongoartifacts "github.com/defos-ai/commander/providers/mongo/artifacts"
	mongoexecutions "github.com/defos-ai/commander/providers/mongo/executions"
	mongomissions "github.com/defos-ai/commander/providers/mongo/missions"
	mongooperations "github.com/defos-ai/commander/providers/mongo/operations"
	mongosessions "github.com/defos-ai/commander/providers/mongo/sessions"
	mongostore "github.com/defos-ai/commander/providers/mongo/store"
	mongotesting "github.com/defos-ai/commander/providers/mongo/testing"
	rabbitmqcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	s3artifacts "github.com/defos-ai/commander/providers/s3/artifacts"
	s3reports "github.com/defos-ai/commander/providers/s3/reports"
	s3store "github.com/defos-ai/commander/providers/s3/store"
	s3testing "github.com/defos-ai/commander/providers/s3/testing"
)

type dependencies struct {
	SessionStore      sessions.Store
	Bus               comms.Bus
	ArtifactStore     artifacts.ArtifactStore
	ArtifactFileStore artifacts.ArtifactFileStore
	AgentStore        agents.AgentStore
	MissionStore      missions.MissionStore
	OperationStore    operations.OperationStore
	ExecutionStore    executions.ExecutionStore
	ActionStore       actions.ActionStore
	ReportStore       reports.ReportStore
	TestCleaner       frameworktesting.TestCleaner
	CatalogStore      agents.CatalogStore
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
	deps.SessionStore, err = deps.createSessionStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("session database: %w", err)
	}
	deps.OperationStore, err = deps.createOperationStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.ExecutionStore, err = deps.createExecutionStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("execution database: %w", err)
	}
	deps.AgentStore, err = deps.createAgentStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url: %w", err)
	}
	deps.CatalogStore, err = deps.createCatalogStore(ctx, configuration.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database-url catalog: %w", err)
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

func (d *dependencies) createCatalogStore(ctx context.Context, rawURL string) (agents.CatalogStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fsagents.NewCatalogFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongoagents.NewCatalogMongoStore(d.mongo), nil
	default:
		return nil, fmt.Errorf("unsupported agent catalog store scheme %q", scheme)
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

func (d *dependencies) createExecutionStore(ctx context.Context, rawURL string) (executions.ExecutionStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(rawURL); scheme {
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

func (d *dependencies) createSessionStore(ctx context.Context, rawURL string) (sessions.Store, error) {
	switch scheme := connectionScheme(rawURL); scheme {
	case "file", "fs":
		path, err := fs.FilesystemPath(rawURL)
		if err != nil {
			return nil, err
		}
		return fssessions.NewSessionFsStore(path)
	case "mongodb", "mongodb+srv":
		if err := d.connectMongo(ctx, rawURL); err != nil {
			return nil, err
		}
		return mongosessions.NewSessionMongoStore(ctx, d.mongo)
	default:
		return nil, fmt.Errorf("unsupported session store scheme %q", scheme)
	}
}
