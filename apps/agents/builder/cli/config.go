package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	builder "github.com/defos-ai/commander/apps/agents/builder"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/host"
)

type config struct {
	actionNames       []string
	busURL            string
	storageURL        string
	workspaceDir      string
	logLevel          string
	logFormat         string
	heartbeatInterval time.Duration
	scope             *environment.DiscoveryScope
	secrets           *agents.SecretConfig
}

func parseConfig(args []string, stderr io.Writer, options Options) (config, error) {
	knownActions := builder.ActionNames()
	availableActions := strings.Join(knownActions, ",")
	var result config
	var actionNames, searchPaths string
	var busURL, storageURL string
	flags := flag.NewFlagSet(serviceName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&actionNames, "actions", "", "Comma-separated action names; omitted uses the agent type's catalog, explicit empty exposes no actions; available: "+availableActions)
	flags.StringVar(&busURL, "bus-url", "", "Message broker URL or filesystem path; overrides BUS_URL; defaults to ./state/bus")
	flags.StringVar(&storageURL, "storage-url", "", "Artifact storage URL or filesystem path; overrides STORAGE_URL; defaults to ./state/storage")
	flags.StringVar(&result.workspaceDir, "workspace-dir", "", "Source workspace directory; overrides BUILDER_WORKSPACE_DIR; defaults to ./state/builder-workspaces")
	flags.StringVar(&result.logLevel, "log-level", "info", "Log level: debug, info, warn, error; overrides LOG_LEVEL")
	flags.StringVar(&result.logFormat, "log-format", "json", "Log format: json or text; overrides LOG_FORMAT; logs are written to stderr")
	flags.DurationVar(&result.heartbeatInterval, "heartbeat-interval", agents.DefaultHeartbeatInterval, "Heartbeat interval; zero disables heartbeats and prevents a later controller from discovering this agent")
	flags.StringVar(&searchPaths, "search-paths", "", "Executable discovery paths separated by the OS path separator; omitted uses PATH, explicit empty searches nowhere")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [flags]\n", serviceName)
		fmt.Fprintln(stderr, "Run one agent independently; connect the default controller to the same bus and artifact storage.")
		fmt.Fprintln(stderr, "A fresh agent ID is created on each start.")
		fmt.Fprintln(stderr, "Set SECRETS_ENCRYPTION_KEY to receive encrypted secrets for this agent type.")
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
	result.busURL = host.Value(busURL, provided["bus-url"], "BUS_URL", filepath.Join("state", "bus"))
	result.storageURL = host.Value(storageURL, provided["storage-url"], "STORAGE_URL", filepath.Join("state", "storage"))
	result.workspaceDir = host.Value(result.workspaceDir, provided["workspace-dir"], "BUILDER_WORKSPACE_DIR", filepath.Join("state", "builder-workspaces"))
	if strings.TrimSpace(result.workspaceDir) == "" {
		return config{}, errors.New("workspace-dir must not be blank")
	}

	result.logLevel = host.Value(result.logLevel, provided["log-level"], "LOG_LEVEL", "info")
	result.logFormat = host.Value(result.logFormat, provided["log-format"], "LOG_FORMAT", "json")
	if _, err := host.NewLogger(io.Discard, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	if result.heartbeatInterval < 0 {
		return config{}, errors.New("heartbeat-interval must not be negative")
	}
	var err error
	cipher := options.SecretCipher
	if cipher == nil {
		if key := os.Getenv("SECRETS_ENCRYPTION_KEY"); key != "" {
			cipher, err = crypto.NewSecretCipher(key)
			if err != nil {
				return config{}, fmt.Errorf("SECRETS_ENCRYPTION_KEY: %w", err)
			}
		}
	}
	if cipher != nil {
		if err := cipher.Validate(); err != nil {
			return config{}, fmt.Errorf("secret cipher: %w", err)
		}
		result.secrets = &agents.SecretConfig{Decryptor: cipher}
	}
	if provided["actions"] {
		result.actionNames, err = parseList(actionNames)
	}
	if err != nil {
		return config{}, fmt.Errorf("actions: %w", err)
	}
	for _, name := range result.actionNames {
		if !slices.Contains(knownActions, name) {
			return config{}, fmt.Errorf("unknown action %q; available: %s", name, availableActions)
		}
	}
	if provided["search-paths"] {
		paths := filepath.SplitList(searchPaths)
		if paths == nil {
			paths = []string{}
		}
		result.scope = &environment.DiscoveryScope{Paths: paths}
	}
	return result, nil
}

func parseList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	items := strings.Split(value, ",")
	for i, item := range items {
		items[i] = strings.TrimSpace(item)
	}
	return items, validateList(items)
}

func validateList(items []string) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return errors.New("list entries must not be empty")
		}
		if seen[item] {
			return fmt.Errorf("duplicate entry %q", item)
		}
		seen[item] = true
	}
	return nil
}
