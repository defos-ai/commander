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

	"commander/adapters/api"
	"commander/framework/host"
)

const defaultAllowedOrigins = "http://localhost:4275,http://127.0.0.1:4275"

type config struct {
	busURL        string
	storageURL    string
	databaseURL   string
	listenAddress string
	security      api.SecurityConfig
	swagger       bool
	logLevel      string
	logFormat     string
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var result config
	var allowedHosts, allowedOrigins, trustedProxies, proxyHeaders string
	flags := flag.NewFlagSet("commander-api", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&result.listenAddress, "listen-address", "", "HTTP host:port (API_LISTEN_ADDRESS, then 0.0.0.0:$PORT, then 127.0.0.1:4274)")
	flags.StringVar(&result.busURL, "bus-url", "", "Message broker connection (BUS_URL; default ./state/bus)")
	flags.StringVar(&result.storageURL, "storage-url", "", "Artifact file and report storage (STORAGE_URL; default ./state/storage)")
	flags.StringVar(&result.databaseURL, "database-url", "", "Database connection (DATABASE_URL; default ./state/database)")
	flags.StringVar(&result.logLevel, "log-level", "", "Logging level: debug, info, warn, error (LOG_LEVEL; default info)")
	flags.StringVar(&result.logFormat, "log-format", "", "Logging format: text or json (LOG_FORMAT; default json)")
	flags.StringVar(&allowedHosts, "allowed-hosts", "", "Comma-separated HTTP hostnames/IPs without ports (API_ALLOWED_HOSTS; default localhost,127.0.0.1,::1)")
	flags.StringVar(&allowedOrigins, "allowed-origins", "", "Comma-separated browser origins (API_ALLOWED_ORIGINS; default "+defaultAllowedOrigins+")")
	flags.StringVar(&trustedProxies, "trusted-proxies", "", "Comma-separated proxy IPs/CIDRs (API_TRUSTED_PROXIES; default none)")
	flags.StringVar(&proxyHeaders, "proxy-headers", "", "Trusted proxy headers: none, forwarded, x-forwarded, x-forwarded-proto (API_PROXY_HEADERS; default none)")
	flags.BoolVar(&result.swagger, "swagger", false, "Enable authenticated Swagger UI and OpenAPI document (API_SWAGGER_ENABLED; default false)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: commander-api [flags]")
		fmt.Fprintln(output, "Read-only missions, actions, agents and controllers API; optional Swagger at /docs/.")
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
	if !provided["swagger"] {
		var err error
		result.swagger, err = strconv.ParseBool(host.Value("", false, "API_SWAGGER_ENABLED", "false"))
		if err != nil {
			return config{}, errors.New("API_SWAGGER_ENABLED must be a boolean")
		}
	}
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
	result.security = api.SecurityConfig{
		Username: os.Getenv("API_USERNAME"), Password: os.Getenv("API_PASSWORD"),
		AllowedHosts:   splitList(host.Value(allowedHosts, provided["allowed-hosts"], "API_ALLOWED_HOSTS", "localhost,127.0.0.1,::1")),
		AllowedOrigins: splitList(host.Value(allowedOrigins, provided["allowed-origins"], "API_ALLOWED_ORIGINS", defaultAllowedOrigins)),
		TrustedProxies: splitList(host.Value(trustedProxies, provided["trusted-proxies"], "API_TRUSTED_PROXIES", "")),
		ProxyHeaders:   host.Value(proxyHeaders, provided["proxy-headers"], "API_PROXY_HEADERS", "none"),
	}
	if err := result.security.Validate(); err != nil {
		return config{}, err
	}
	if _, err := host.NewLogger(output, result.logLevel, result.logFormat); err != nil {
		return config{}, err
	}
	return result, nil
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	values := strings.Split(value, ",")
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values
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
