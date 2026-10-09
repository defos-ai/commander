package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/defos-ai/commander/framework/host"
)

const missingCredentials = `MCP_TOKEN must be set and not blank.
Set credentials, then repeat your startup command:

  export MCP_TOKEN="$(openssl rand -hex 32)"

PowerShell:
  $env:MCP_TOKEN = [guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N')`

func resolveCredentials(supplied *host.BearerCredentials) (host.BearerCredentials, error) {
	value := host.BearerCredentials{Token: os.Getenv("MCP_TOKEN")}
	if supplied != nil {
		value = *supplied
	}
	return value, validateCredentials(value)
}

func validateCredentials(value host.BearerCredentials) error {
	err := value.Validate()
	if errors.Is(err, host.ErrMissingCredentials) {
		return errors.New(missingCredentials)
	}
	if err != nil {
		return fmt.Errorf("MCP_TOKEN: %w", err)
	}
	return nil
}

func lookupCredentials() (host.BearerCredentials, error) {
	if runtime.GOOS != "linux" {
		return resolveCredentials(nil)
	}
	current, err := os.Stat("/proc/self/exe")
	if err != nil {
		return resolveCredentials(nil)
	}
	running, err := os.Stat("/proc/1/exe")
	if err != nil || !os.SameFile(current, running) {
		return resolveCredentials(nil)
	}
	// Docker exec does not inherit a token exported before the server's exec.
	environment, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return host.BearerCredentials{}, fmt.Errorf("read MCP process environment: %w", err)
	}
	var value host.BearerCredentials
	for _, variable := range strings.Split(string(environment), "\x00") {
		name, content, _ := strings.Cut(variable, "=")
		if name == "MCP_TOKEN" {
			value.Token = content
		}
	}
	return value, validateCredentials(value)
}

func showCredentials(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("commander-mcp credentials", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander-mcp credentials")
		fmt.Fprintln(stderr, "Print the running container's MCP token, or the token from the current environment. Does not start MCP.")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	value, err := lookupCredentials()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Token: %s\n", value.Token)
	return err
}
