package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func showStatus(ctx context.Context, args []string, output, stderr io.Writer) error {
	flags := flag.NewFlagSet("commander status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("state-dir", env("COMMANDER_STATE_DIR", ""), "Commander state directory; defaults to ~/.commander")
	asJSON := flags.Bool("json", false, "Print machine-readable instance status without credentials")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander status [--json] [--state-dir directory]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: commander status [--json] [--state-dir directory]")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := stateDirectory(*directory)
	if err != nil {
		return err
	}
	instance, err := local.Discover(ctx, filepath.Join(resolved, "local"))
	if err != nil && !errors.Is(err, local.ErrNotRunning) {
		return err
	}
	if err == nil {
		return printStatus(&instance, *asJSON, output)
	}
	return printStatus(nil, *asJSON, output)
}

func printStatus(instance *local.Instance, asJSON bool, output io.Writer) error {
	status := struct {
		Status string `json:"status"`
		*local.Instance
	}{Status: "stopped"}
	if instance != nil {
		status.Status, status.Instance = "running", instance
	}
	if asJSON {
		return json.NewEncoder(output).Encode(status)
	}
	if status.Instance == nil {
		_, err := fmt.Fprintln(output, "Commander is not running.")
		return err
	}
	message := fmt.Sprintf("Commander is running\nInstance: %s\nPID: %d\nMCP: %s\nAPI: %s\n", instance.ID, instance.PID, instance.MCPURL, instance.APIURL)
	if instance.AdminURL != "" {
		message += "Admin: " + instance.AdminURL + "\n"
	}
	_, err := io.WriteString(output, message)
	return err
}
