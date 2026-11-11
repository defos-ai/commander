package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func manageLocal(ctx context.Context, command string, args []string, output, stderr io.Writer) error {
	flags := flag.NewFlagSet("commander "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("state-dir", env("COMMANDER_STATE_DIR", ""), "Commander state directory; defaults to ~/.commander")
	asJSON := flags.Bool("json", false, "Print machine-readable status without credentials")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: commander %s [flags]\n", command)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; run commander %s --help", command)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := stateDirectory(*directory)
	if err != nil {
		return err
	}
	if command == "start" {
		instance, err := local.StartBackground(ctx, resolved)
		if err != nil {
			return err
		}
		if instance.AdminURL == "" {
			fmt.Fprintf(stderr, "Admin UI unavailable; Commander is running. See %s for details.\n", filepath.Join(resolved, "local", "hosts.log"))
		}
		return printStatus(&instance, *asJSON, output)
	}
	shutdown, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := local.Stop(shutdown, filepath.Join(resolved, "local")); err != nil {
		return fmt.Errorf("stop Commander: %w", err)
	}
	if instance, err := local.Discover(ctx, filepath.Join(resolved, "local")); err == nil {
		return printStatus(&instance, *asJSON, output)
	} else if !errors.Is(err, local.ErrNotRunning) {
		return err
	}
	return printStatus(nil, *asJSON, output)
}
