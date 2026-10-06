package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

type config struct {
	listenAddress string
	staticDir     string
}

func parseConfig(args []string, output io.Writer) (config, error) {
	var result config
	flags := flag.NewFlagSet("commander-ui", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&result.listenAddress, "listen-address", "", "HTTP host:port (UI_LISTEN_ADDRESS, then :$PORT, then :4275)")
	flags.StringVar(&result.staticDir, "static-dir", "", "Built frontend directory (UI_STATIC_DIR; default ./frontend/dist)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: commander-ui [flags]")
		fmt.Fprintln(output, "Serve the Commander admin frontend.")
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
	if !provided["listen-address"] {
		if value, present := os.LookupEnv("UI_LISTEN_ADDRESS"); present {
			result.listenAddress = value
		} else if port, present := os.LookupEnv("PORT"); present {
			if !validPort(port, false) {
				return config{}, errors.New("PORT must be an integer between 1 and 65535")
			}
			result.listenAddress = ":" + port
		} else {
			result.listenAddress = ":4275"
		}
	}
	_, port, err := net.SplitHostPort(result.listenAddress)
	if err != nil || !validPort(port, true) {
		return config{}, errors.New("listen-address must be a host:port address with a port between 0 and 65535")
	}
	if !provided["static-dir"] {
		if value, present := os.LookupEnv("UI_STATIC_DIR"); present {
			result.staticDir = value
		} else {
			result.staticDir = "./frontend/dist"
		}
	}
	if strings.TrimSpace(result.staticDir) == "" {
		return config{}, errors.New("static-dir must not be blank")
	}
	return result, nil
}

func validPort(value string, allowZero bool) bool {
	if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		return false
	}
	port, err := strconv.Atoi(value)
	return err == nil && port <= 65535 && (port > 0 || allowZero)
}
