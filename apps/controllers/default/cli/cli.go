package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"commander/apps/controllers/default/data"
	"commander/framework/controller"
	adapterconsumer "commander/framework/controller/consumers/adapter"
	"commander/framework/controller/runner"
	"commander/framework/host"
	artifactprovider "commander/providers/artifact"
	commsprovider "commander/providers/comms"
	controllerprovider "commander/providers/controller"
)

type Options struct {
	Ready func()
}

func Run(ctx context.Context, args []string, stderr io.Writer) error {
	return RunWithOptions(ctx, args, stderr, Options{})
}

func RunWithOptions(ctx context.Context, args []string, stderr io.Writer, options Options) (result error) {
	if stderr == nil {
		stderr = io.Discard
	}
	configuration, err := parseConfig(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger, err := host.NewLogger(stderr, configuration.logLevel, configuration.logFormat)
	if err != nil {
		return err
	}
	logger = logger.With("service", "commander-controller", "controller_type", controllerType, "controller_id", configuration.controllerID)
	defer func() {
		if result != nil {
			logger.Error("Controller stopped with error", "error", result)
		} else {
			logger.Info("Controller stopped")
		}
	}()
	return serve(ctx, configuration, logger, options)
}

func serve(ctx context.Context, configuration config, logger *slog.Logger, options Options) (result error) {
	bus, err := commsprovider.OpenBus(ctx, configuration.busURL)
	if err != nil {
		return fmt.Errorf("bus-url: %w", err)
	}
	defer func() { result = errors.Join(result, bus.Close()) }()
	storage, err := artifactprovider.OpenStorage(ctx, configuration.storageURL, artifactprovider.StorageOptions{})
	if err != nil {
		return fmt.Errorf("storage-url: %w", err)
	}
	defer func() { result = errors.Join(result, storage.Close()) }()
	reportStorage, err := controllerprovider.OpenReportStorage(ctx, configuration.storageURL)
	if err != nil {
		return fmt.Errorf("storage-url: %w", err)
	}
	defer func() { result = errors.Join(result, reportStorage.Close()) }()
	database, err := controllerprovider.OpenDatabase(ctx, configuration.databaseURL)
	if err != nil {
		return fmt.Errorf("database-url: %w", err)
	}
	defer func() { result = errors.Join(result, database.Close()) }()
	if configuration.withMissions != "" {
		if err := data.UpsertMissions(ctx, database.MissionStore, configuration.withMissions); err != nil {
			return fmt.Errorf("with-missions: %w", err)
		}
	}
	// Keep routing available until the runner has suspended its accepted tasks.
	coordination, err := controller.NewController(context.WithoutCancel(ctx), bus, controller.ControllerConfig{
		Type: controllerType, ID: configuration.controllerID, HeartbeatTimeout: configuration.heartbeatTimeout,
		ArtifactStore: storage.ArtifactStore, AgentStore: database.AgentStore, ActionStore: database.ActionStore,
		ExecutionStore: database.ExecutionStore, Logger: logger,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, coordination.Close(shutdown))
	}()
	execution, err := runner.NewRunner(context.WithoutCancel(ctx), coordination, runner.Config{
		MissionStore: database.MissionStore, OperationStore: database.OperationStore,
		ReportStore: reportStorage.ReportStore, ArtifactRestorer: coordination, AgentCatalog: coordination,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, execution.Close(shutdown))
	}()
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	requests, err := adapterconsumer.NewConsumer(ctx, bus, controllerType, coordination, execution, logger)
	if err != nil {
		return err
	}
	requestsDone := make(chan struct{})
	var requestErr error
	go func() {
		defer close(requestsDone)
		requestErr = requests.Consume(requestCtx)
	}()
	defer func() {
		cancelRequests()
		result = errors.Join(result, requests.Close())
		<-requestsDone
		if requestErr != nil && !errors.Is(requestErr, context.Canceled) {
			result = errors.Join(result, requestErr)
		}
	}()
	logger.Info("Controller ready", "transport", "bus")
	if options.Ready != nil {
		options.Ready()
	}
	select {
	case <-ctx.Done():
		logger.Info("Controller shutting down")
		return nil
	case <-coordination.Done():
		return fmt.Errorf("controller coordination stopped: %w", coordination.Err())
	case <-requestsDone:
		return errors.New("adapter consumer stopped")
	}
}

func Main(args []string, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stderr); err != nil {
		fmt.Fprintln(stderr, "commander-controller:", err)
		return 1
	}
	return 0
}
