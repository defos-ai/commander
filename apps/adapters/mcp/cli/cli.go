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

	missionsmcp "commander/adapters/mcp"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/host"
	"commander/framework/id"
	artifactprovider "commander/providers/artifact"
	commsprovider "commander/providers/comms"
	controllerprovider "commander/providers/controller"
	testprovider "commander/providers/testing"
)

const adapterType = "mcp"

type Options struct {
	Ready func(net.Addr)
}

// Run installs no signal handlers; Main adds process-level shutdown handling.
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
	adapterID, err := id.AdapterID(adapterType)
	if err != nil {
		return err
	}
	logger = logger.With("service", "commander-mcp", "adapter_type", adapterType, "adapter_id", adapterID)
	defer func() {
		if result != nil {
			logger.Error("MCP server stopped with error", "error", result)
		} else {
			logger.Info("MCP server stopped")
		}
	}()
	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return serve(ctx, configuration, adapterID, logger, listener, options)
}

// Controller work continues when an MCP client disconnects or this host stops.
func serve(ctx context.Context, configuration config, adapterID string, logger *slog.Logger, listener net.Listener, options Options) (result error) {
	defer listener.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
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
	controller, err := controllercommands.NewClient(ctx, bus, adapterID, logger)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, controller.Close()) }()
	serverConfig := missionsmcp.Config{
		Operations: controller, Logger: logger,
		AgentStore: database.AgentStore, ExecutionStore: database.ExecutionStore,
		MissionStore: database.MissionStore, OperationStore: database.OperationStore,
		ActionStore: database.ActionStore, ReportStore: reportStorage.ReportStore, ArtifactStore: storage.ArtifactStore,
	}
	if os.Getenv("ENV") == "tests" {
		cleaner, err := testprovider.OpenCleaner(configuration.storageURL, configuration.databaseURL)
		if err != nil {
			return fmt.Errorf("test cleanup: %w", err)
		}
		serverConfig.TestCleaner = &testCleaner{missions: database.MissionStore, state: cleaner}
	}
	server, err := missionsmcp.NewServer(controller, serverConfig)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", newMCPHandler(server, logger, configuration.allowedHosts))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		check, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, err := database.OperationStore.List(check); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{\"status\":\"ok\"}\n")
	})
	httpServer := &http.Server{
		Handler: logRequests(logger, mux), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	defer func() { result = errors.Join(result, httpServer.Close()) }()
	finished := make(chan error, 1)
	go func() { finished <- httpServer.Serve(listener) }()
	logger.Info("MCP server listening", "address", listener.Addr().String(), "endpoint", "/mcp", "transport", "streamable-http")
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
		logger.Info("MCP server shutting down")
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			logger.Warn("HTTP shutdown timed out; closing active requests", "error", err)
			result = errors.Join(result, httpServer.Close())
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
		return result
	}
}

func Main(args []string, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stderr); err != nil && !(ctx.Err() != nil && cancellationOnly(err)) {
		fmt.Fprintln(stderr, "commander-mcp:", err)
		return 1
	}
	return 0
}

// A signal is a normal shutdown only when it is the sole cause of failure.
// Joined storage or transport cleanup failures must still fail the process.
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
