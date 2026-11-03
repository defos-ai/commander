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

	"github.com/defos-ai/commander/framework/controller"
	adapterconsumer "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/workers/runner"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/controllers"
	"github.com/defos-ai/commander/framework/host"
)

type Options struct {
	Ready        func()
	SecretCipher *crypto.SecretCipher
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
	decryptor, err := secretDecryptor(options.SecretCipher)
	if err != nil {
		return err
	}
	deps, err := openDependencies(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, deps.Close()) }()
	agentSelector := runner.RoundRobinSelector{}
	registration, err := controller.Register(ctx, deps.ControllerStore, controller.RegistrationConfig{
		ID: configuration.controllerID, Type: controllerType,
		AgentSelector: agentSelector.Name(), Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("controller registration: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, registration.Close(shutdown))
	}()
	// Keep routing available until the runner has suspended its accepted tasks.
	coordination, err := controller.NewController(context.WithoutCancel(ctx), deps.Bus, controller.ControllerConfig{
		Type: controllerType, ID: configuration.controllerID, HeartbeatTimeout: configuration.heartbeatTimeout,
		ArtifactStore: deps.ArtifactStore, AgentStore: deps.AgentStore, ActionStore: deps.ActionStore,
		ExecutionStore: deps.ExecutionStore, Logger: logger,
		SessionStore: deps.SessionStore, OperationStore: deps.OperationStore,
		SecretStore: deps.SecretStore, SecretRequestStore: deps.SecretRequestStore,
		SecretDecryptor: decryptor,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, coordination.Close(shutdown))
	}()
	execution, err := runner.New(context.WithoutCancel(ctx), coordination, runner.Dependencies{
		MissionStore: deps.MissionStore, OperationStore: deps.OperationStore,
		Sessions:    coordination.RuntimeSessions(),
		ReportStore: deps.ReportStore, AgentCatalog: coordination,
		AgentSelector: agentSelector, Logger: logger,
		TaskTimeout: configuration.taskTimeout,
	})
	if err != nil {
		return err
	}
	runnerDone := make(chan struct{})
	var runnerErr error
	go func() {
		defer close(runnerDone)
		runnerErr = execution.Run(context.WithoutCancel(ctx))
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, execution.Close(shutdown))
		select {
		case <-runnerDone:
			// Close can win before the Run goroutine starts.
			if runnerErr != nil && runnerErr != runner.ErrClosed && !errors.Is(result, runnerErr) {
				result = errors.Join(result, runnerErr)
			}
		case <-shutdown.Done():
			if !errors.Is(result, shutdown.Err()) {
				result = errors.Join(result, shutdown.Err())
			}
		}
	}()
	commandCtx, cancelCommands := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelCommands()
	commands, err := adapterconsumer.NewConsumer(ctx, deps.Bus, controllerType, coordination.ID(), coordination, execution, logger)
	if err != nil {
		return err
	}
	commandsDone := make(chan struct{})
	var commandErr error
	go func() {
		defer close(commandsDone)
		commandErr = commands.Consume(commandCtx)
	}()
	defer func() {
		cancelCommands()
		result = errors.Join(result, commands.Close())
		<-commandsDone
		if commandErr != nil && !errors.Is(commandErr, context.Canceled) {
			result = errors.Join(result, commandErr)
		}
	}()
	if err := registration.SetStatus(ctx, controllers.Ready, ""); err != nil {
		return fmt.Errorf("controller ready registration: %w", err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		result = errors.Join(result, registration.SetStatus(shutdown, controllers.Draining, ""))
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
	case <-runnerDone:
		return errors.New("operation runner stopped")
	case <-commandsDone:
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
