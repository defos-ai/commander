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
	// Mark registration draining before the controller stops command admission.
	coordination, err := controller.NewController(context.WithoutCancel(ctx), deps.Bus, controller.ControllerConfig{
		Type: controllerType, ID: configuration.controllerID, HeartbeatTimeout: configuration.heartbeatTimeout,
		ArtifactStore: deps.ArtifactStore, AgentStore: deps.AgentStore, ActionStore: deps.ActionStore,
		ExecutionStore: deps.ExecutionStore, Logger: logger,
		SessionStore: deps.SessionStore, OperationStore: deps.OperationStore,
		MissionStore: deps.MissionStore, ReportStore: deps.ReportStore,
		AgentSelector: agentSelector, TaskTimeout: configuration.taskTimeout,
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
