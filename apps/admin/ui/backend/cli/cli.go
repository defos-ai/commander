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
	"time"

	"github.com/defos-ai/commander/apps/admin/ui/backend/connections"
	"github.com/defos-ai/commander/apps/admin/ui/backend/static"
)

type Options struct {
	Ready func(net.Addr)
	// Help receives usage text; server diagnostics use the Run output writer.
	Help io.Writer
}

type ConfigError struct{ cause error }

func (e *ConfigError) Error() string { return e.cause.Error() }
func (e *ConfigError) Unwrap() error { return e.cause }

func Run(ctx context.Context, args []string, output io.Writer) error {
	return RunWithOptions(ctx, args, output, Options{})
}

func RunWithOptions(ctx context.Context, args []string, output io.Writer, options Options) error {
	if output == nil {
		output = io.Discard
	}
	help := options.Help
	if help == nil {
		help = output
	}
	configuration, err := parseConfig(args, help)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return &ConfigError{cause: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	handler, err := static.NewHandler(configuration.staticDir)
	if err != nil {
		return err
	}
	defer handler.Close()
	connectionHandler, err := connections.NewHandler(configuration.connections)
	if err != nil {
		return &ConfigError{cause: err}
	}
	defer connectionHandler.Close()
	routes := http.NewServeMux()
	routes.Handle("/ui", connectionHandler)
	routes.Handle("/ui/", connectionHandler)
	routes.Handle("/", handler)
	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("service", "commander-ui")
	server := &http.Server{
		Handler: routes, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second,
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	defer server.Close()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("UI server listening", "address", listener.Addr().String())
	if options.Ready != nil {
		options.Ready(listener.Addr())
	}
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		logger.Info("UI server shutting down")
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Info("UI server stopped")
		return nil
	}
}
