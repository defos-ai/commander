package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/defos-ai/commander/framework/host"
)

type config struct {
	busURL        string
	storageURL    string
	databaseURL   string
	listenAddress string
	runtime       string
	allowedHosts  []string
	logLevel      string
	logFormat     string
	credentials   host.BearerCredentials
}

func parseConfig(args []string, stderr io.Writer, credentials *host.BearerCredentials) (config, error) {
	var result config
	var allowedHosts, busURL, storageURL, databaseURL string
	flags := flag.NewFlagSet("commander-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&busURL, "bus-url", "", "Message broker URL: filesystem path or file:///path (fallback: BUS_URL, then ./state/bus)")
	flags.StringVar(&storageURL, "storage-url", "", "Artifact and report storage URL (fallback: STORAGE_URL, then ./state/storage)")
	flags.StringVar(&databaseURL, "database-url", "", "Database URL for querying saved state (fallback: DATABASE_URL, then ./state/database)")
	flags.StringVar(&result.listenAddress, "listen-address", "", "HTTP host:port serving /mcp, /healthz and /aboutz (MCP_LISTEN_ADDRESS, then 0.0.0.0:$PORT, then 127.0.0.1:4273)")
	flags.StringVar(&result.runtime, "runtime", "", "Deployment runtime: native, docker or docker-compose (MCP_RUNTIME; default native)")
	flags.StringVar(&allowedHosts, "allowed-hosts", "localhost,127.0.0.1,::1", "Comma-separated HTTP hostnames/IPs without ports; include the public hostname when deploying behind an ingress")
	flags.StringVar(&result.logLevel, "log-level", "info", "Logging level: debug, info, warn, error (fallback: LOG_LEVEL, then info)")
	flags.StringVar(&result.logFormat, "log-format", "json", "Logging format: json, text (fallback: LOG_FORMAT, then json)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander-mcp [flags]")
		fmt.Fprintln(stderr, "Send commands to the controller over the bus and query its stores; serve /mcp, /healthz and /aboutz.")
		fmt.Fprintln(stderr, "Use the same bus-url, storage-url and database-url as commander-controller.")
		fmt.Fprintln(stderr, "Set MCP_TOKEN for bearer authentication. Use 'commander-mcp credentials' to look it up.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments; run commander-mcp --help")
	}
	var err error
	result.credentials, err = resolveCredentials(credentials)
	if err != nil {
		return config{}, err
	}
	provided := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { provided[value.Name] = true })
	result.busURL = host.Value(busURL, provided["bus-url"], "BUS_URL", filepath.Join("state", "bus"))
	result.storageURL = host.Value(storageURL, provided["storage-url"], "STORAGE_URL", filepath.Join("state", "storage"))
	result.databaseURL = host.Value(databaseURL, provided["database-url"], "DATABASE_URL", filepath.Join("state", "database"))
	result.runtime = host.Value(result.runtime, provided["runtime"], "MCP_RUNTIME", "native")
	switch result.runtime {
	case "native", "docker", "docker-compose":
	default:
		return config{}, errors.New("runtime must be native, docker or docker-compose (MCP_RUNTIME)")
	}
	result.listenAddress = host.Value(result.listenAddress, provided["listen-address"], "MCP_LISTEN_ADDRESS", "")
	if result.listenAddress == "" && !provided["listen-address"] {
		result.listenAddress = "127.0.0.1:4273"
		if port, present := os.LookupEnv("PORT"); present {
			portNumber, err := strconv.Atoi(port)
			if err != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsFunc(port, func(r rune) bool { return r < '0' || r > '9' }) {
				return config{}, errors.New("PORT must be an integer between 1 and 65535")
			}
			result.listenAddress = net.JoinHostPort("0.0.0.0", port)
		}
	}

	hostname, port, err := net.SplitHostPort(result.listenAddress)
	if err != nil || strings.TrimSpace(hostname) != hostname {
		return config{}, errors.New("listen-address must be a host:port address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return config{}, errors.New("listen-address port must be between 0 and 65535")
	}
	for _, host := range strings.Split(allowedHosts, ",") {
		host = normalizeHost(strings.TrimSpace(host))
		if !validHostname(host) {
			return config{}, errors.New("allowed-hosts must contain hostnames or IPs without ports, URLs, or wildcards")
		}
		result.allowedHosts = append(result.allowedHosts, host)
	}
	result.logLevel = host.Value(result.logLevel, provided["log-level"], "LOG_LEVEL", "info")
	result.logFormat = host.Value(result.logFormat, provided["log-format"], "LOG_FORMAT", "json")
	if _, err := host.NewLogger(io.Discard, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	return result, nil
}
