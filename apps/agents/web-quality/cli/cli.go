package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	webquality "commander/apps/agents/web-quality"
	agents "commander/framework/agent"
	"commander/framework/agent/action"
	"commander/framework/host"
	"commander/framework/id"
	artifactprovider "commander/providers/artifact"
	commsprovider "commander/providers/comms"
)

const serviceName = "commander-" + string(webquality.WebQualityAgentType) + "-agent"

// Run installs no signal handlers; Main adds process-level shutdown handling.
func Run(ctx context.Context, args []string, stderr io.Writer) (result error) {
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
	agentID, err := id.AgentID(string(webquality.WebQualityAgentType))
	if err != nil {
		return err
	}
	logger = logger.With("service", serviceName, "agent_type", webquality.WebQualityAgentType, "agent_id", agentID)
	defer func() {
		if result != nil && !cancellationOnly(result) {
			logger.Error("agent stopped with error", "error", result)
		} else {
			logger.Info("agent stopped")
		}
	}()
	storage, err := artifactprovider.OpenStorage(ctx, configuration.storageURL, artifactprovider.StorageOptions{AgentID: agentID})
	if err != nil {
		return fmt.Errorf("storage-url: %w", err)
	}
	defer func() { result = errors.Join(result, storage.Close()) }()
	actions := webquality.NewActions()
	if configuration.actionNames != nil {
		selected := make([]action.Executor, 0, len(configuration.actionNames))
		for _, name := range configuration.actionNames {
			for _, instance := range actions {
				if instance.Action().Name == name {
					selected = append(selected, instance)
					break
				}
			}
		}
		actions = selected
	}
	agent, err := webquality.NewWebQualityAgent(agents.AgentConfig{
		ID:                 agentID,
		Actions:            actions,
		GrantedPermissions: configuration.permissions,
		ArtifactStore:      storage.ArtifactStore, Logger: logger,
		InlineOutputLimit: &configuration.inlineLimit, HeartbeatInterval: &configuration.heartbeatInterval,
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bus, err := commsprovider.OpenBus(ctx, configuration.busURL)
	if err != nil {
		return fmt.Errorf("bus-url: %w", err)
	}
	defer func() { result = errors.Join(result, bus.Close()) }()
	logger.Info("agent starting", "heartbeat_interval", configuration.heartbeatInterval)
	return agent.Serve(ctx, bus, configuration.scope)
}

func Main(args []string, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// NotifyContext supplies a descriptive signal cause in newer Go versions.
	// Translate process signals at this boundary into ordinary cancellation,
	// without changing Run's handling of caller causes or joined cleanup errors.
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

// A signal is a normal shutdown only when it is the sole cause of failure.
// Joined persistence or transport errors must still fail the CLI.
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
