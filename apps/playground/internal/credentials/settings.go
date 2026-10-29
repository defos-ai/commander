package credentials

import (
	"errors"
	"flag"
	"io"
	"strconv"
)

func ContainerState(arguments []string, environment map[string]string) (string, bool, error) {
	directory := environment["COMMANDER_STATE_DIR"]
	var temporary bool
	flags := flag.NewFlagSet("commander", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&directory, "state-dir", directory, "")
	flags.BoolVar(&temporary, "temporary", false, "")
	for _, name := range []string{"mcp-url", "api-url", "admin-url", "provider", "model", "base-url"} {
		flags.String(name, "", "")
	}
	for _, name := range []string{"all-in-one", "serve-admin-ui", "debug", "no-color"} {
		flags.Bool(name, false, "")
	}
	if flags.Parse(arguments) != nil || flags.NArg() != 0 {
		return "", false, errors.New("cannot identify Commander container startup settings")
	}
	provided := false
	flags.Visit(func(value *flag.Flag) { provided = provided || value.Name == "temporary" })
	if value := environment["COMMANDER_TEMPORARY"]; !provided && value != "" {
		var err error
		temporary, err = strconv.ParseBool(value)
		if err != nil {
			return "", false, errors.New("cannot identify Commander container temporary setting")
		}
	}
	return directory, temporary, nil
}
