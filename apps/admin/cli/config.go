package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/defos-ai/commander/apps/admin/client"
)

type config struct {
	connection client.Connection
	arguments  []string
	json       bool
	dryRun     bool
	scopeID    string
	ownerID    string
	requestID  string
}

func configuration(args []string, options Options) (config, error) {
	var result config
	if options.Connection != nil {
		result.connection = *options.Connection
	} else {
		result.connection.URL = env("COMMANDER_API_URL", "http://localhost:4274")
		result.connection.Credentials.Username = env("COMMANDER_API_USERNAME", "commander")
		result.connection.Credentials.Password = os.Getenv("COMMANDER_API_PASSWORD")
	}
	flags := flag.NewFlagSet("commander-admin", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.StringVar(&result.connection.URL, "api-url", result.connection.URL, "Commander API URL")
	flags.StringVar(&result.connection.Credentials.Username, "api-username", result.connection.Credentials.Username, "Commander API username")
	flags.BoolVar(&result.json, "json", false, "Print command results as JSON")
	flags.BoolVar(&result.dryRun, "dry-run", false, "Preview a mission import or seed without writing")
	flags.StringVar(&result.scopeID, "scope-id", "", "Filter sessions by scope UUID")
	flags.StringVar(&result.ownerID, "owner-agent-id", "", "Filter sessions by exact process owner")
	flags.StringVar(&result.requestID, "request-id", "", "Session control request UUID")
	var err error
	result.arguments, err = parseArguments(flags, args)
	if err != nil {
		return result, err
	}
	sessionOptions := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "scope-id" || option.Name == "owner-agent-id" || option.Name == "request-id" {
			sessionOptions = true
			if option.Value.String() == "" {
				err = errors.New("session options require nonempty values")
			}
		}
	})
	if sessionOptions && (len(result.arguments) == 0 || result.arguments[0] != "sessions") {
		err = errors.New("session options require a sessions command")
	}
	return result, err
}

// Accept options around subcommands and operands; -- keeps later values literal.
func parseArguments(flags *flag.FlagSet, args []string) ([]string, error) {
	var operands []string
	for len(args) != 0 {
		argument := args[0]
		if argument == "--" {
			return append(operands, args[1:]...), nil
		}
		if argument == "-" || !strings.HasPrefix(argument, "-") {
			operands = append(operands, argument)
			args = args[1:]
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(argument, "-"), "-"), "=")
		count := 1
		if option := flags.Lookup(name); option != nil && !hasValue {
			boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
			if (!ok || !boolean.IsBoolFlag()) && len(args) > 1 {
				count++
			}
		}
		if err := flags.Parse(args[:count]); err != nil {
			return operands, err
		}
		args = args[count:]
	}
	return operands, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
