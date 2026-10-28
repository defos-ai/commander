package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/controllers"
	sessionmodel "github.com/defos-ai/commander/framework/domain/sessions"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	"github.com/defos-ai/commander/providers/fs/agents"
	"github.com/defos-ai/commander/providers/fs/artifacts"
	"github.com/defos-ai/commander/providers/fs/comms"
	fscontrollers "github.com/defos-ai/commander/providers/fs/controllers"
	"github.com/defos-ai/commander/providers/fs/executions"
	"github.com/defos-ai/commander/providers/fs/missions"
	"github.com/defos-ai/commander/providers/fs/operations"
	"github.com/defos-ai/commander/providers/fs/reports"
	"github.com/defos-ai/commander/providers/fs/sessions"
)

type catalog struct{ actions.ActionStore }

func (c catalog) List(ctx context.Context) ([]actions.Action, error) {
	if err := probe(ctx, "/catalog"); err != nil {
		return nil, err
	}
	return c.ActionStore.List(ctx)
}

type sessionStore struct{ sessionmodel.Store }

func (s sessionStore) Apply(ctx context.Context, change sessionmodel.Change) (sessionmodel.Record, error) {
	if err := probe(ctx, "/sessions"); err != nil {
		return sessionmodel.Record{}, err
	}
	return s.Store.Apply(ctx, change)
}

func probe(ctx context.Context, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, os.Getenv("FIXTURE_PEER")+path, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		return errors.New("fixture storage temporarily unavailable")
	}
	return nil
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root := os.Getenv("DATABASE_URL")
	bus := must(comms.NewFsBus(os.Getenv("BUS_URL")))
	defer bus.Close()
	operationStore := must(operations.NewOperationFsStore(root))
	coordination := must(controller.NewController(context.Background(), bus, controller.ControllerConfig{
		ID: "default-controller-00000001", HeartbeatTimeout: 6 * time.Second,
		AgentStore: must(agents.NewAgentFsStore(root)), ActionStore: catalog{must(fsactions.NewActionFsStore(root))},
		ArtifactStore: must(artifacts.NewArtifactFsStore(root)), ExecutionStore: must(executions.NewExecutionFsStore(root)),
		SessionStore: sessionStore{must(sessions.NewSessionFsStore(root))}, OperationStore: operationStore,
	}))
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = coordination.Close(shutdown)
	}()
	execution := must(runner.NewRunner(context.Background(), coordination, runner.Config{
		MissionStore: must(missions.NewMissionFsStore(root)), OperationStore: operationStore,
		ReportStore: must(reports.NewReportFsStore(os.Getenv("STORAGE_URL"))), AgentCatalog: coordination, Sessions: coordination.RuntimeSessions(),
	}))
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = execution.Close(shutdown)
	}()
	commands := must(adapter.NewConsumer(ctx, bus, "default", coordination.ID(), coordination, execution, nil))
	commandsDone := make(chan error, 1)
	go func() { commandsDone <- commands.Consume(ctx) }()
	defer func() { cancel(); _ = commands.Close(); <-commandsDone }()
	registration := must(controller.Register(ctx, must(fscontrollers.NewControllerFsStore(root)), controller.RegistrationConfig{
		ID: coordination.ID(), Type: "default", AgentSelector: "round-robin", RuleSelector: "mission",
	}))
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = registration.Close(shutdown)
	}()
	if err := registration.SetStatus(ctx, controllers.Ready, ""); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case <-coordination.Done():
		return coordination.Err()
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
