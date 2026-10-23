package cli

import (
	"flag"
	"io"
	"os"

	"github.com/defos-ai/commander/apps/admin/client"
)

type config struct {
	connection client.Connection
	arguments  []string
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
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	result.arguments = flags.Args()
	return result, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
