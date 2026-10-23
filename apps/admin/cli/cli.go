// Package cli runs administrative commands without starting Playground or a model.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/defos-ai/commander/apps/admin/client"
	uicli "github.com/defos-ai/commander/apps/admin/ui/backend/cli"
)

// Options supplies caller-owned connection and UI readiness without changing environment.
type Options struct {
	Connection *client.Connection
	// Ready receives the bound address when serve starts accepting connections.
	Ready func(net.Addr)
}

// UsageError identifies invalid command arguments separately from runtime errors.
type UsageError struct{ cause error }

func (e *UsageError) Error() string { return e.cause.Error() }
func (e *UsageError) Unwrap() error { return e.cause }

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunWithOptions(ctx, args, stdin, stdout, stderr, Options{})
}

func RunWithOptions(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, options Options) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "serve" {
		return runServe(ctx, args[1:], stdout, stderr, options)
	}
	if len(args) == 2 && args[0] == "help" && args[1] == "serve" {
		return runServe(ctx, []string{"--help"}, stdout, stderr, options)
	}
	configuration, err := configuration(args, options)
	if errors.Is(err, flag.ErrHelp) {
		if len(configuration.arguments) > 0 && configuration.arguments[0] == "missions" {
			return missionHelp(stdout)
		}
		if len(configuration.arguments) > 0 && configuration.arguments[0] == "secrets" {
			return secretHelp(stdout)
		}
		return help(stdout)
	}
	if err != nil {
		return &UsageError{cause: err}
	}
	if len(configuration.arguments) == 0 || len(configuration.arguments) == 1 && configuration.arguments[0] == "help" {
		return help(stdout)
	}
	if configuration.arguments[0] == "missions" {
		return runMissions(ctx, configuration, stdin, stdout)
	}
	if configuration.arguments[0] == "secrets" {
		return runSecrets(ctx, configuration, stdin, stdout, stderr)
	}
	if len(configuration.arguments) == 2 && configuration.arguments[0] == "help" && configuration.arguments[1] == "missions" {
		return missionHelp(stdout)
	}
	if len(configuration.arguments) == 2 && configuration.arguments[0] == "help" && configuration.arguments[1] == "secrets" {
		return secretHelp(stdout)
	}
	return &UsageError{cause: errors.New("unknown command or arguments; run commander-admin --help")}
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer, options Options) error {
	err := uicli.RunWithOptions(ctx, args, stderr, uicli.Options{Ready: options.Ready, Help: stdout})
	var configuration *uicli.ConfigError
	if errors.As(err, &configuration) {
		return &UsageError{cause: err}
	}
	return err
}

func help(output io.Writer) error {
	_, err := io.WriteString(output, `Usage: commander-admin [options] <command>
       commander-admin serve [UI options]

Administer Commander without starting Playground or configuring a model.

Commands:
  help                  Show this help
  missions              List, export, validate, import or seed missions
  secrets               List requests and manage agent secrets
  serve                 Serve the Admin UI

API connection:
  --api-url URL         API address (COMMANDER_API_URL; default http://localhost:4274)
  --api-username NAME   API username (COMMANDER_API_USERNAME; default commander)
  COMMANDER_API_PASSWORD supplies the password; no password flag is accepted.

Options:
  --json                Print command results as JSON
  -h, --help            Show this help

Run commander-admin <command> --help for command options.
`)
	return err
}

// Main returns 0 for success/help, 2 for usage errors and 1 for runtime failures.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "commander-admin:", err)
		var usage *UsageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	return 0
}
