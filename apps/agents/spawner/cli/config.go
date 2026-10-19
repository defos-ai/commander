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

	spawner "github.com/defos-ai/commander/apps/agents/spawner"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

type config struct {
	actionNames         []string
	agentID             string
	busURL              string
	storageURL          string
	stateDir            string
	spawnAgents         []string
	registrationTimeout time.Duration
	logLevel            string
	logFormat           string
	heartbeatInterval   time.Duration
	scope               *environment.DiscoveryScope
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	knownActions := spawner.ActionNames()
	availableActions := strings.Join(knownActions, ",")
	var result config
	var actionNames, searchPaths string
	var busURL, storageURL string
	flags := flag.NewFlagSet(serviceName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&actionNames, "actions", "", "Comma-separated action names; omitted uses the agent type's catalog, explicit empty exposes no actions; available: "+availableActions)
	flags.StringVar(&busURL, "bus-url", "", "Message broker URL or filesystem path; overrides BUS_URL; defaults to ./state/bus")
	flags.StringVar(&storageURL, "storage-url", "", "Artifact storage URL or filesystem path; overrides STORAGE_URL; defaults to ./state/storage")
	flags.StringVar(&result.stateDir, "state-dir", "", "Launch records and downloaded packages; overrides SPAWNER_STATE_DIR; defaults to ./state/spawner")
	flags.Func("spawn-agents", "Public GitHub agent package to start; repeat for multiple agents: github.com/owner/repository@vMAJOR.MINOR.PATCH", func(value string) error {
		if _, err := packages.ParseReference(value); err != nil {
			return err
		}
		result.spawnAgents = append(result.spawnAgents, value)
		return nil
	})
	flags.DurationVar(&result.registrationTimeout, "registration-timeout", 30*time.Second, "Maximum wait for each spawned agent to register")
	flags.StringVar(&result.logLevel, "log-level", "info", "Log level: debug, info, warn, error; overrides LOG_LEVEL")
	flags.StringVar(&result.logFormat, "log-format", "json", "Log format: json or text; overrides LOG_FORMAT; logs are written to stderr")
	flags.DurationVar(&result.heartbeatInterval, "heartbeat-interval", agent.DefaultHeartbeatInterval, "Heartbeat interval; zero disables heartbeats and prevents a later controller from discovering this agent")
	flags.StringVar(&searchPaths, "search-paths", "", "Executable discovery paths separated by the OS path separator; omitted uses PATH, explicit empty searches nowhere")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [flags]\n", serviceName)
		fmt.Fprintln(stderr, "Run a spawner agent and launch native agent packages on the same bus and artifact storage.")
		fmt.Fprintln(stderr, "A fresh agent ID is created unless COMMANDER_AGENT_ID_SUFFIX supplies eight lowercase hex characters.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional argument; use --help for available flags")
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	result.busURL = host.Value(busURL, provided["bus-url"], "BUS_URL", filepath.Join("state", "bus"))
	result.storageURL = host.Value(storageURL, provided["storage-url"], "STORAGE_URL", filepath.Join("state", "storage"))
	result.stateDir = host.Value(result.stateDir, provided["state-dir"], "SPAWNER_STATE_DIR", filepath.Join("state", "spawner"))
	if strings.TrimSpace(result.stateDir) == "" {
		return config{}, errors.New("state-dir must not be blank")
	}
	result.logLevel = host.Value(result.logLevel, provided["log-level"], "LOG_LEVEL", "info")
	result.logFormat = host.Value(result.logFormat, provided["log-format"], "LOG_FORMAT", "json")
	if _, err := host.NewLogger(io.Discard, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	if result.heartbeatInterval < 0 {
		return config{}, errors.New("heartbeat-interval must not be negative")
	}
	if result.registrationTimeout <= 0 {
		return config{}, errors.New("registration-timeout must be positive")
	}
	var err error
	if suffix := os.Getenv("COMMANDER_AGENT_ID_SUFFIX"); suffix != "" {
		result.agentID, err = id.AgentIDFromSuffix(string(spawner.SpawnerAgentType), suffix)
		if err != nil {
			return config{}, fmt.Errorf("COMMANDER_AGENT_ID_SUFFIX: %w", err)
		}
	}
	if provided["actions"] {
		result.actionNames, err = parseList(actionNames)
		if err != nil {
			return config{}, fmt.Errorf("actions: %w", err)
		}
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
	result.busURL, err = normalizeConnection(result.busURL, "bus-url", "amqp", "amqps")
	if err != nil {
		return config{}, err
	}
	result.storageURL, err = normalizeConnection(result.storageURL, "storage-url", "s3")
	if err != nil {
		return config{}, err
	}
	return result, nil
}

func parseList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	items := strings.Split(value, ",")
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, errors.New("list entries must not be empty")
		}
		if seen[item] {
			return nil, fmt.Errorf("duplicate entry %q", item)
		}
		items[i] = item
		seen[item] = true
	}
	return items, nil
}

func childEnvironment(storageURL string) map[string]string {
	names := []string{"PATH", "SystemRoot", "WINDIR", "TMPDIR", "TEMP", "TMP"}
	if connectionScheme(storageURL) == "s3" {
		names = append(names, "AWS_ENDPOINT_URL_S3", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "S3_USE_PATH_STYLE")
	}
	environment := make(map[string]string, len(names))
	for _, name := range names {
		if value, exists := os.LookupEnv(name); exists {
			environment[name] = value
		}
	}
	return environment
}
