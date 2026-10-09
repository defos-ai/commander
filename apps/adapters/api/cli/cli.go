package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

const adapterType = "api"

type Options struct {
	Ready func(net.Addr)
	// Embedded hosts can supply a credential pair without changing process environment.
	Credentials *host.BasicCredentials
}

func Run(ctx context.Context, args []string, stderr io.Writer) error {
	return RunWithOptions(ctx, args, stderr, Options{})
}

func RunWithOptions(ctx context.Context, args []string, stderr io.Writer, options Options) (result error) {
	if stderr == nil {
		stderr = io.Discard
	}
	configuration, err := parseConfig(args, stderr, options.Credentials)
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
	adapterID, err := id.AdapterID(adapterType)
	if err != nil {
		return err
	}
	logger = logger.With("service", "commander-api", "adapter_type", adapterType, "adapter_id", adapterID)
	defer func() {
		if result != nil {
			logger.Error("API server stopped with error", "error", result)
		} else {
			logger.Info("API server stopped")
		}
	}()
	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	deps, err := openDependencies(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, deps.Close()) }()

	// Let accepted HTTP requests finish before closing their controller client.
	lifetime, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	stopOnStartupCancel := context.AfterFunc(ctx, stop)
	defer stopOnStartupCancel()
	controller, err := controllercommands.NewClient(lifetime, deps.Bus, adapterID, logger)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, controller.Close()) }()
	handler, err := api.NewHandler(api.Config{
		Security: configuration.security, SwaggerEnabled: configuration.swagger,
		Operations: controller, MissionStore: deps.MissionStore, OperationStore: deps.OperationStore,
		AgentStore: deps.AgentStore, ActionStore: deps.ActionStore, ReportStore: deps.ReportStore,
		ControllerStore: deps.ControllerStore,
		ArtifactStore:   deps.ArtifactStore, ArtifactFileStore: deps.ArtifactFileStore,
		TestCleaner: deps.TestCleaner,
	})
	if err != nil {
		return err
	}
	stopOnStartupCancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return serve(ctx, listener, handler, logger, options)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger, options Options) (result error) {
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	defer func() { result = errors.Join(result, server.Close()) }()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("API server listening", "address", listener.Addr().String(), "transport", "http")
	if options.Ready != nil {
		options.Ready(listener.Addr())
	}
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("API server shutting down")
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			logger.Warn("HTTP shutdown timed out; closing active requests")
			result = errors.Join(result, err, server.Close())
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
		return result
	}
}

func Main(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) > 0 && args[0] == "credentials" {
		if err := showCredentials(args[1:], stdout, stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "commander-api:", err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stderr); err != nil && !(ctx.Err() != nil && cancellationOnly(err)) {
		fmt.Fprintln(stderr, "commander-api:", err)
		return 1
	}
	return 0
}

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
