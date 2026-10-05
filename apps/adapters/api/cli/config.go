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
	"unicode"

	"commander/framework/host"
)

type config struct {
	busURL        string
	storageURL    string
	databaseURL   string
	listenAddress string
	username      string
	password      string
	logLevel      string
	logFormat     string
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var result config
	flags := flag.NewFlagSet("commander-api", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&result.listenAddress, "listen-address", "", "HTTP host:port (API_LISTEN_ADDRESS, then 0.0.0.0:$PORT, then 127.0.0.1:4274)")
	flags.StringVar(&result.busURL, "bus-url", "", "Message broker connection (BUS_URL; default ./state/bus)")
	flags.StringVar(&result.storageURL, "storage-url", "", "Artifact file and report storage (STORAGE_URL; default ./state/storage)")
	flags.StringVar(&result.databaseURL, "database-url", "", "Database connection (DATABASE_URL; default ./state/database)")
	flags.StringVar(&result.logLevel, "log-level", "", "Logging level: debug, info, warn, error (LOG_LEVEL; default info)")
	flags.StringVar(&result.logFormat, "log-format", "", "Logging format: text or json (LOG_FORMAT; default json)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: commander-api [flags]")
		fmt.Fprintln(output, "API host foundation: serves /healthz and /readyz. Business endpoints follow in later increments.")
		fmt.Fprintln(output, "API_USERNAME and API_PASSWORD must be supplied through the environment.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}
	provided := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { provided[value.Name] = true })
	result.busURL = host.Value(result.busURL, provided["bus-url"], "BUS_URL", filepath.Join("state", "bus"))
	result.storageURL = host.Value(result.storageURL, provided["storage-url"], "STORAGE_URL", filepath.Join("state", "storage"))
	result.databaseURL = host.Value(result.databaseURL, provided["database-url"], "DATABASE_URL", filepath.Join("state", "database"))
	result.logLevel = host.Value(result.logLevel, provided["log-level"], "LOG_LEVEL", "info")
	result.logFormat = host.Value(result.logFormat, provided["log-format"], "LOG_FORMAT", "json")
	result.listenAddress = host.Value(result.listenAddress, provided["listen-address"], "API_LISTEN_ADDRESS", "")
	if result.listenAddress == "" && !provided["listen-address"] {
		result.listenAddress = "127.0.0.1:4274"
		if port, present := os.LookupEnv("PORT"); present {
			if !validPort(port, false) {
				return config{}, errors.New("PORT must be an integer between 1 and 65535")
			}
			result.listenAddress = net.JoinHostPort("0.0.0.0", port)
		}
	}
	if err := validateListenAddress(result.listenAddress); err != nil {
		return config{}, err
	}
	result.username, result.password = os.Getenv("API_USERNAME"), os.Getenv("API_PASSWORD")
	if strings.TrimSpace(result.username) == "" || strings.TrimSpace(result.password) == "" {
		return config{}, errors.New("API_USERNAME and API_PASSWORD must both be set and not blank")
	}
	if strings.Contains(result.username, ":") || strings.ContainsFunc(result.username, unicode.IsControl) || strings.ContainsFunc(result.password, unicode.IsControl) {
		return config{}, errors.New("API credentials must not contain control characters; API_USERNAME must not contain ':'")
	}
	if _, err := host.NewLogger(output, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	return result, nil
}

func validateListenAddress(value string) error {
	hostname, port, err := net.SplitHostPort(value)
	if err != nil || !validPort(port, true) || strings.ContainsAny(hostname, "/@?#") || strings.ContainsFunc(hostname, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return errors.New("listen-address must be a host:port address with a port between 0 and 65535")
	}
	return nil
}

func validPort(value string, allowZero bool) bool {
	if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		return false
	}
	port, err := strconv.Atoi(value)
	return err == nil && port <= 65535 && (port > 0 || allowZero)
}
