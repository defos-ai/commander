package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/defos-ai/commander/framework/host"
)

func showCredentials(args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(output, "Usage: commander credentials api|mcp [--raw]\n--raw prints only the API password or MCP token.")
		return err
	}
	if len(args) == 0 || (args[0] != "api" && args[0] != "mcp") {
		return errors.New("usage: commander credentials api|mcp")
	}
	var raw, help bool
	for _, option := range args[1:] {
		switch option {
		case "--raw":
			raw = true
		case "--help", "-h":
			help = true
		default:
			return errors.New("usage: commander credentials api|mcp")
		}
	}
	if help {
		_, err := fmt.Fprintln(output, "Usage: commander credentials api|mcp [--raw]\n--raw prints only the API password or MCP token.")
		return err
	}
	environment, err := credentialEnvironment()
	if err != nil {
		return err
	}
	if args[0] == "api" {
		value := host.BasicCredentials{Username: environment["API_USERNAME"], Password: environment["API_PASSWORD"]}
		if err := value.Validate(); err != nil {
			if errors.Is(err, host.ErrMissingCredentials) {
				return errors.New("API_USERNAME and API_PASSWORD must be set in this environment; for generated native all-in-one credentials, use /credentials inside Playground")
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
	value := host.BearerCredentials{Token: environment["MCP_TOKEN"]}
	if err := value.Validate(); err != nil {
		if errors.Is(err, host.ErrMissingCredentials) {
			return errors.New("MCP_TOKEN must be set in this environment; for a generated native all-in-one token, use /credentials inside Playground")
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

func credentialEnvironment() (map[string]string, error) {
	environment := os.Environ()
	container, err := commanderAtPIDOne()
	if err != nil {
		return nil, err
	}
	if container {
		// Docker exec does not inherit exports from the entrypoint. Tini retains
		// them because credentials are generated before it starts as PID 1.
		data, err := os.ReadFile("/proc/1/environ")
		if err != nil {
			return nil, fmt.Errorf("read Commander container environment: %w", err)
		}
		environment = strings.Split(string(data), "\x00")
	}
	values := make(map[string]string, 3)
	for _, variable := range environment {
		name, value, _ := strings.Cut(variable, "=")
		switch name {
		case "API_USERNAME", "API_PASSWORD", "MCP_TOKEN":
			values[name] = value
		}
	}
	return values, nil
}

func commanderAtPIDOne() (bool, error) {
	if runtime.GOOS != "linux" {
		return false, nil
	}
	current, err := os.Stat("/proc/self/exe")
	if err != nil {
		return false, nil
	}
	running, err := os.Stat("/proc/1/exe")
	if err != nil {
		return false, nil
	}
	if os.SameFile(current, running) {
		return true, nil
	}
	init, err := os.Stat("/usr/bin/tini")
	if err != nil || !os.SameFile(init, running) {
		return false, nil
	}
	data, err := os.ReadFile("/proc/1/cmdline")
	if err != nil {
		return false, fmt.Errorf("identify Commander container: %w", err)
	}
	args := strings.Split(string(data), "\x00")
	if len(args) < 4 || args[1] != "--" || args[3] != "--all-in-one" {
		return false, nil
	}
	command, err := os.Stat(args[2])
	return err == nil && os.SameFile(current, command), nil
}
