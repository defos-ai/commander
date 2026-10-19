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
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/host"
	"github.com/defos-ai/commander/framework/id"
)

type config struct {
	actionNames         []string
	agentID             string
	busURL              string
	storageURL          string
	stateDir            string
	runtime             string
	dockerNetwork       string
	spawnAgents         []string
	registrationTimeout time.Duration
	logLevel            string
	logFormat           string
	heartbeatInterval   time.Duration
	secrets             *agent.SecretConfig
}

func parseConfig(args []string, stderr io.Writer, options Options) (config, error) {
	knownActions := spawner.ActionNames()
	availableActions := strings.Join(knownActions, ",")
	var result config
	var actionNames, agentIDSuffix string
	var busURL, storageURL string
	flags := flag.NewFlagSet(serviceName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&agentIDSuffix, "agent-id-suffix", "", "8 lowercase hex characters; overrides COMMANDER_AGENT_ID_SUFFIX; empty generates a fresh ID")
	flags.StringVar(&actionNames, "actions", "", "Comma-separated action names; omitted uses the agent type's catalog, explicit empty exposes no actions; available: "+availableActions)
	flags.StringVar(&busURL, "bus-url", "", "Message broker URL or filesystem path; overrides BUS_URL; defaults to ./state/bus")
	flags.StringVar(&storageURL, "storage-url", "", "Artifact storage URL or filesystem path; overrides STORAGE_URL; defaults to ./state/storage")
	flags.Func("spawn-agents", "Public GitHub repository URL or package to start; omitted version means stable; optional [/module/path] and @stable or @vMAJOR.MINOR.PATCH; repeat for multiple agents", func(value string) error {
		if _, err := packages.ParseSelector(value); err != nil {
			return err
		}
		result.spawnAgents = append(result.spawnAgents, value)
		return nil
	})
	flags.DurationVar(&result.registrationTimeout, "registration-timeout", 30*time.Second, "Maximum wait for each spawned agent to register; Docker defaults to 45s to allow the next heartbeat")
	flags.StringVar(&result.logLevel, "log-level", "info", "Log level: debug, info, warn, error; overrides LOG_LEVEL")
	flags.StringVar(&result.logFormat, "log-format", "json", "Log format: json or text; overrides LOG_FORMAT; logs are written to stderr")
	flags.DurationVar(&result.heartbeatInterval, "heartbeat-interval", agent.DefaultHeartbeatInterval, "Heartbeat interval; zero disables heartbeats and prevents a later controller from discovering this agent")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [flags]\n", serviceName)
		fmt.Fprintln(stderr, "Run a spawner agent and launch agent packages on the same bus and artifact storage.")
		fmt.Fprintln(stderr, "A fresh agent ID is created unless an agent ID suffix is supplied.")
		fmt.Fprintln(stderr, "Set SECRETS_ENCRYPTION_KEY to receive encrypted secrets for this agent type.")
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
	result.stateDir = host.Value("", false, "SPAWNER_STATE_DIR", filepath.Join("state", "spawner"))
	result.runtime = host.Value("", false, "SPAWNER_RUNTIME", "native")
	if options.Runtime != "" {
		result.runtime = options.Runtime
	}
	if options.StateDir != "" {
		result.stateDir = options.StateDir
	}
	if result.runtime != "native" && result.runtime != "docker" {
		return config{}, errors.New("SPAWNER_RUNTIME must be native or docker")
	}
	if result.runtime == "docker" {
		result.dockerNetwork = os.Getenv("SPAWNER_DOCKER_NETWORK")
		if strings.TrimSpace(result.dockerNetwork) == "" {
			return config{}, errors.New("SPAWNER_DOCKER_NETWORK is required for the docker runtime")
		}
		if err := docker.ValidateNetwork(result.dockerNetwork); err != nil {
			return config{}, fmt.Errorf("SPAWNER_DOCKER_NETWORK: %w", err)
		}
		if !provided["registration-timeout"] {
			result.registrationTimeout = 45 * time.Second
		}
	}
	if strings.TrimSpace(result.stateDir) == "" {
		return config{}, errors.New("SPAWNER_STATE_DIR must not be blank")
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
	if suffix := host.Value(agentIDSuffix, provided["agent-id-suffix"], "COMMANDER_AGENT_ID_SUFFIX", ""); suffix != "" {
		result.agentID, err = id.AgentIDFromSuffix(string(spawner.AgentType), suffix)
		if err != nil {
			return config{}, fmt.Errorf("agent-id-suffix / COMMANDER_AGENT_ID_SUFFIX: %w", err)
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
	result.busURL, err = normalizeConnection(result.busURL, "bus-url", "amqp", "amqps")
	if err != nil {
		return config{}, err
	}
	result.storageURL, err = normalizeConnection(result.storageURL, "storage-url", "s3")
	if err != nil {
		return config{}, err
	}
	if result.runtime == "docker" {
		if scheme := connectionScheme(result.busURL); scheme != "amqp" && scheme != "amqps" {
			return config{}, errors.New("docker runtime requires an amqp or amqps bus-url reachable from spawned containers")
		}
		if connectionScheme(result.storageURL) != "s3" {
			return config{}, errors.New("docker runtime requires an s3 storage-url reachable from spawned containers")
		}
	}
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
		result.secrets = &agent.SecretConfig{Decryptor: cipher}
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

func childEnvironment(storageURL, runtime string) map[string]string {
	var names []string
	if runtime == "native" {
		names = []string{"PATH", "SystemRoot", "WINDIR", "TMPDIR", "TEMP", "TMP", "CHROMIUM_PATH", "CHROMIUM_NO_SANDBOX"}
	}
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
