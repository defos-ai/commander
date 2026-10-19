package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	spawner "github.com/defos-ai/commander/apps/agents/spawner"
	"github.com/defos-ai/commander/apps/agents/spawner/native"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

const serviceName = "commander-" + string(spawner.SpawnerAgentType)

// Options supplies dependencies for an embedded host.
type Options struct {
	Resolver *packages.Resolver
}

// Run installs no signal handlers; Main adds process-level shutdown handling.
func Run(ctx context.Context, args []string, stderr io.Writer) error {
	return RunWithOptions(ctx, args, stderr, Options{})
}

// RunWithOptions runs the host with injected dependencies and no signal handlers.
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
	agentID := configuration.agentID
	if agentID == "" {
		agentID, err = id.AgentID(string(spawner.SpawnerAgentType))
		if err != nil {
			return err
		}
	}
	logger = logger.With("service", serviceName, "agent_type", spawner.SpawnerAgentType, "agent_id", agentID)
	defer func() {
		if result != nil && !cancellationOnly(result) {
			logger.Error("agent stopped with error", "error", result)
		} else {
			logger.Info("agent stopped")
		}
	}()
	dependencies, err := openDependencies(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, dependencies.Close()) }()
	resolver := options.Resolver
	if resolver == nil {
		resolver = packages.NewResolver(nil)
		defer resolver.CloseIdleConnections()
	}
	manager, err := native.NewManager(ctx, native.Config{
		Directory:           configuration.stateDir,
		Bus:                 dependencies.Bus,
		BusURL:              configuration.busURL,
		StorageURL:          configuration.storageURL,
		Environment:         childEnvironment(configuration.storageURL),
		Resolver:            resolver,
		RegistrationTimeout: configuration.registrationTimeout,
	})
	if err != nil {
		return err
	}
	// Manager shutdown awaits its children while their bus is still open.
	defer func() { result = errors.Join(result, manager.Close()) }()
	selected := []action.Executor{}
	if configuration.actionNames == nil || len(configuration.actionNames) > 0 {
		selected, err = spawner.NewActions(manager)
		if err != nil {
			return err
		}
	}
	if configuration.actionNames != nil {
		catalog := selected
		selected = make([]action.Executor, 0, len(configuration.actionNames))
		for _, name := range configuration.actionNames {
			for _, executor := range catalog {
				if executor.Action().Name == name {
					selected = append(selected, executor)
					break
				}
			}
		}
	}
	instance, err := spawner.NewSpawnerAgent(agent.AgentConfig{
		ID:                agentID,
		Actions:           selected,
		ArtifactFileStore: dependencies.ArtifactFileStore,
		Logger:            logger,
		HeartbeatInterval: &configuration.heartbeatInterval,
	}, manager)
	if err != nil {
		return err
	}
	for index, reference := range configuration.spawnAgents {
		if _, err := manager.Spawn(ctx, "startup:"+strconv.Itoa(index), reference); err != nil {
			return fmt.Errorf("spawn startup agent %d: %w", index+1, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger.Info("agent starting", "heartbeat_interval", configuration.heartbeatInterval)
	return instance.Serve(ctx, dependencies.Bus, configuration.scope)
}

func Main(args []string, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(context.WithoutCancel(signalCtx))
	stopForwarding := context.AfterFunc(signalCtx, cancel)
	defer func() {
		stopForwarding()
		cancel()
	}()
	if err := Run(ctx, args, stderr); err != nil {
		if ctx.Err() != nil && cancellationOnly(err) {
			return 0
		}
		fmt.Fprintln(stderr, serviceName+":", err)
		return 1
	}
	return 0
}

// Joined persistence or transport errors must survive ordinary cancellation.
func cancellationOnly(err error) bool {
	if err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !cancellationOnly(child) {
				return false
			}
		}
		return true
	}
	if inner := errors.Unwrap(err); inner != nil {
		return cancellationOnly(inner)
	}
	return false
}
