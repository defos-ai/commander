package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"commander/framework/controller"
	"commander/framework/host"
	"commander/framework/id"
)

const controllerType = "default"

type config struct {
	controllerID     string
	busURL           string
	storageURL       string
	databaseURL      string
	withMissions     string
	logLevel         string
	logFormat        string
	heartbeatTimeout time.Duration
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	var result config
	var heartbeat string
	flags := flag.NewFlagSet("commander-controller", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&result.busURL, "bus-url", "", "Broker URL (BUS_URL; default ./state/bus)")
	flags.StringVar(&result.storageURL, "storage-url", "", "Artifact and report storage URL (STORAGE_URL; default ./state/storage)")
	flags.StringVar(&result.databaseURL, "database-url", "", "Inventory, executions and operations database URL (DATABASE_URL; default ./state/database)")
	flags.StringVar(&result.withMissions, "with-missions", "", "JSON mission file to upsert on startup; omitted loads no missions")
	flags.StringVar(&result.logLevel, "log-level", "info", "Logging level: debug, info, warn, error (LOG_LEVEL)")
	flags.StringVar(&result.logFormat, "log-format", "json", "Logging format: json, text (LOG_FORMAT)")
	flags.StringVar(&heartbeat, "heartbeat-timeout", controller.DefaultHeartbeatTimeout.String(), "Agent lease duration (HEARTBEAT_TIMEOUT)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander-controller [flags]")
		fmt.Fprintln(stderr, "Coordinate agents and operations. Adapters send requests through the shared broker.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional argument: %s", flags.Arg(0))
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	for _, field := range []struct {
		name, env, fallback string
		value               *string
	}{
		{"bus-url", "BUS_URL", "state/bus", &result.busURL},
		{"storage-url", "STORAGE_URL", "state/storage", &result.storageURL},
		{"database-url", "DATABASE_URL", "state/database", &result.databaseURL},
		{"log-level", "LOG_LEVEL", "info", &result.logLevel},
		{"log-format", "LOG_FORMAT", "json", &result.logFormat},
		{"heartbeat-timeout", "HEARTBEAT_TIMEOUT", controller.DefaultHeartbeatTimeout.String(), &heartbeat},
	} {
		*field.value = host.Value(*field.value, provided[field.name], field.env, field.fallback)
	}
	var err error
	result.controllerID, err = id.ControllerID(controllerType)
	if err != nil {
		return config{}, err
	}
	result.heartbeatTimeout, err = time.ParseDuration(heartbeat)
	if err != nil || result.heartbeatTimeout <= 0 {
		return config{}, errors.New("heartbeat-timeout must be a positive duration")
	}
	if _, err := host.NewLogger(io.Discard, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	return result, nil
}
