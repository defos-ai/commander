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

	builder "github.com/defos-ai/commander/apps/agents/builder"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

const serviceName = "commander-" + string(builder.AgentType)

// Options supplies dependencies for an embedded host.
type Options struct {
	// SecretCipher overrides SECRETS_ENCRYPTION_KEY when non-nil.
	SecretCipher *crypto.SecretCipher
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
		agentID, err = id.AgentID(string(builder.AgentType))
		if err != nil {
			return err
		}
	}
	logger = logger.With("service", serviceName, "agent_type", builder.AgentType, "agent_id", agentID)
	defer func() {
		if result != nil && !cancellationOnly(result) {
			logger.Error("agent stopped with error", "error", result)
		} else {
			logger.Info("agent stopped")
		}
	}()
	actions := []action.Executor{}
	if configuration.actionNames == nil || len(configuration.actionNames) > 0 {
		actions, err = builder.NewActions(configuration.workspaceDir)
		if err != nil {
			return err
		}
	}
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
	dependencies, err := openDependencies(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, dependencies.Close()) }()
	agent, err := builder.NewAgent(agents.AgentConfig{
		ID:                agentID,
		Actions:           actions,
		ArtifactFileStore: dependencies.ArtifactFileStore,
		Logger:            logger,
		HeartbeatInterval: &configuration.heartbeatInterval,
		Secrets:           configuration.secrets,
	}, configuration.workspaceDir)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger.Info("agent starting", "heartbeat_interval", configuration.heartbeatInterval)
	return agent.Serve(ctx, dependencies.Bus, configuration.scope)
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
