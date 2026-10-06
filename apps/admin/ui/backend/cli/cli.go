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

	"commander/admin/ui/static"
)

type Options struct {
	Ready func(net.Addr)
}

func Run(ctx context.Context, args []string, output io.Writer) error {
	return RunWithOptions(ctx, args, output, Options{})
}

func RunWithOptions(ctx context.Context, args []string, output io.Writer, options Options) error {
	if output == nil {
		output = io.Discard
	}
	configuration, err := parseConfig(args, output)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	handler, err := static.NewHandler(configuration.staticDir)
	if err != nil {
		return err
	}
	defer handler.Close()
	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("service", "commander-ui")
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
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

func Main(args []string, output io.Writer) int {
	if output == nil {
		output = io.Discard
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, output); err != nil {
		fmt.Fprintln(output, "commander-ui:", err)
		return 1
	}
	return 0
}
