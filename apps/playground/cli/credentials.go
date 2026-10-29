package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/defos-ai/commander/apps/playground/internal/credentials"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/framework/host"
)

const credentialUsage = "Usage: commander credentials api|mcp [--raw] [--state-dir directory]\n--raw prints only the API password or MCP token."

func showCredentials(args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(output, credentialUsage)
		return err
	}
	if len(args) == 0 || (args[0] != "api" && args[0] != "mcp") {
		return errors.New("usage: commander credentials api|mcp")
	}
	var raw, help bool
	var directory string
	var directoryProvided bool
	for i := 1; i < len(args); i++ {
		option := args[i]
		switch option {
		case "--raw":
			raw = true
		case "--help", "-h":
			help = true
		case "--state-dir":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "--") {
				return errors.New("usage: commander credentials api|mcp")
			}
			directory, directoryProvided = args[i], true
		default:
			if !strings.HasPrefix(option, "--state-dir=") || len(option) == len("--state-dir=") {
				return errors.New("usage: commander credentials api|mcp")
			}
			directory, directoryProvided = strings.TrimPrefix(option, "--state-dir="), true
		}
	}
	if help {
		_, err := fmt.Fprintln(output, credentialUsage)
		return err
	}
	environment, temporary, err := credentialEnvironment()
	if err != nil {
		return err
	}
	if !directoryProvided {
		directory = environment["COMMANDER_STATE_DIR"]
	}
	api := host.BasicCredentials{Username: environment["API_USERNAME"], Password: environment["API_PASSWORD"]}
	mcp := host.BearerCredentials{Token: environment["MCP_TOKEN"]}
	if !temporary && ((args[0] == "api" && api.Password == "") || (args[0] == "mcp" && mcp.Token == "")) {
		directory, err = stateDirectory(directory)
		if err != nil {
			return err
		}
		savedAPI, savedMCP, readErr := local.SavedCredentials(filepath.Join(directory, "local"))
		err = readErr
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read saved all-in-one credentials: %w", err)
		}
		if err == nil {
			if args[0] == "api" && api.Username != "" && api.Username != savedAPI.Username {
				return errors.New("API_USERNAME conflicts with saved all-in-one credentials")
			}
			api, mcp = savedAPI, savedMCP
		}
	}
	if args[0] == "api" {
		value := api
		if err := value.Validate(); err != nil {
			if errors.Is(err, host.ErrMissingCredentials) {
				return errors.New("API_USERNAME and API_PASSWORD must be set in this environment or saved under --state-dir; temporary all-in-one credentials use /credentials inside Playground")
			}
			return fmt.Errorf("API_USERNAME/API_PASSWORD: %w", err)
		}
		if raw {
			_, err = fmt.Fprintln(output, value.Password)
			return err
		}
		_, err = fmt.Fprintf(output, "Username: %s\nPassword: %s\n", value.Username, value.Password)
		return err
	}
	value := mcp
	if err := value.Validate(); err != nil {
		if errors.Is(err, host.ErrMissingCredentials) {
			return errors.New("MCP_TOKEN must be set in this environment or saved under --state-dir; a temporary all-in-one token uses /credentials inside Playground")
		}
		return fmt.Errorf("MCP_TOKEN: %w", err)
	}
	if raw {
		_, err = fmt.Fprintln(output, value.Token)
		return err
	}
	_, err = fmt.Fprintf(output, "Token: %s\n", value.Token)
	return err
}

func credentialEnvironment() (map[string]string, bool, error) {
	environment := os.Environ()
	arguments, err := commanderArguments()
	if err != nil {
		return nil, false, err
	}
	if arguments != nil {
		// An identified container's startup overrides remain authoritative.
		data, err := os.ReadFile("/proc/1/environ")
		if err != nil {
			return nil, false, fmt.Errorf("read Commander container environment: %w", err)
		}
		environment = strings.Split(string(data), "\x00")
	}
	values := make(map[string]string, 5)
	for _, variable := range environment {
		name, value, _ := strings.Cut(variable, "=")
		switch name {
		case "API_USERNAME", "API_PASSWORD", "MCP_TOKEN", "COMMANDER_STATE_DIR", "COMMANDER_TEMPORARY":
			values[name] = value
		}
	}
	if arguments != nil {
		directory, temporary, err := credentials.ContainerState(arguments, values)
		values["COMMANDER_STATE_DIR"] = directory
		return values, temporary, err
	}
	return values, false, nil
}

func commanderArguments() ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	current, err := os.Stat("/proc/self/exe")
	if err != nil {
		return nil, nil
	}
	running, err := os.Stat("/proc/1/exe")
	if err != nil {
		return nil, nil
	}
	direct := os.SameFile(current, running)
	if !direct {
		init, err := os.Stat("/usr/bin/tini")
		if err != nil || !os.SameFile(init, running) {
			return nil, nil
		}
	}
	data, err := os.ReadFile("/proc/1/cmdline")
	if err != nil {
		return nil, fmt.Errorf("identify Commander container: %w", err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	if direct {
		return args[1:], nil
	}
	if len(args) < 4 || args[1] != "--" || args[3] != "--all-in-one" {
		return nil, nil
	}
	command, err := os.Stat(args[2])
	if err != nil || !os.SameFile(current, command) {
		return nil, nil
	}
	return args[3:], nil
}
