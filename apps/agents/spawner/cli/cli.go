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
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

const serviceName = "commander-" + string(spawner.SpawnerAgentType)

// ErrShutdown tells an embedded host to retain shared state after cleanup fails.
var ErrShutdown = errors.New("spawner shutdown failed")

// Options lets an embedded host isolate its runtime and receive startup identities.
type Options struct {
	Runtime  string
	StateDir string
	// Ready reports observed startup agents before serving. The caller must still
	// wait for the controller to persist these identities before using them.
	Ready func(agentID string, spawned []runtimes.Result)
}

// Run installs no signal handlers; Main adds process-level shutdown handling.
func Run(ctx context.Context, args []string, stderr io.Writer) (result error) {
	return RunWithOptions(ctx, args, stderr, Options{})
}

func RunWithOptions(ctx context.Context, args []string, stderr io.Writer, options Options) (result error) {
	if stderr == nil {
		stderr = io.Discard
	}
	configuration, err := parseConfig(args, stderr, options)
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
	defer func() {
		if err := dependencies.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("%w: %w", ErrShutdown, err))
		}
	}()
	selected := []action.Executor{}
	if configuration.actionNames == nil || len(configuration.actionNames) > 0 {
		selected, err = spawner.NewActions(dependencies.Runtime)
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
	}, dependencies.Runtime)
	if err != nil {
		return err
	}
	spawned := make([]runtimes.Result, 0, len(configuration.spawnAgents))
	for index, reference := range configuration.spawnAgents {
		result, err := dependencies.Runtime.Spawn(ctx, "startup:"+strconv.Itoa(index), reference)
		if err != nil {
			return fmt.Errorf("spawn startup agent %d: %w", index+1, err)
		}
		spawned = append(spawned, result)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Ready != nil {
		options.Ready(agentID, spawned)
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
