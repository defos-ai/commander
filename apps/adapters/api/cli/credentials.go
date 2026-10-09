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

const missingCredentials = `API_USERNAME and API_PASSWORD must both be set and not blank.
Set credentials, then repeat your startup command:

  export API_USERNAME=commander
  export API_PASSWORD="$(openssl rand -hex 32)"

PowerShell:
  $env:API_USERNAME = 'commander'
  $env:API_PASSWORD = [guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N')`

func resolveCredentials(supplied *host.BasicCredentials) (host.BasicCredentials, error) {
	value := host.BasicCredentials{Username: os.Getenv("API_USERNAME"), Password: os.Getenv("API_PASSWORD")}
	if supplied != nil {
		value = *supplied
	}
	return value, validateCredentials(value)
}

func validateCredentials(value host.BasicCredentials) error {
	err := value.Validate()
	if errors.Is(err, host.ErrMissingCredentials) {
		return errors.New(missingCredentials)
	}
	if err != nil {
		return fmt.Errorf("API_USERNAME/API_PASSWORD: %w", err)
	}
	return nil
}

func lookupCredentials() (host.BasicCredentials, error) {
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
	// Docker exec does not inherit credentials exported before the server's exec.
	environment, err := os.ReadFile("/proc/1/environ")
	if err != nil {
		return host.BasicCredentials{}, fmt.Errorf("read API process environment: %w", err)
	}
	var value host.BasicCredentials
	for _, variable := range strings.Split(string(environment), "\x00") {
		name, content, _ := strings.Cut(variable, "=")
		switch name {
		case "API_USERNAME":
			value.Username = content
		case "API_PASSWORD":
			value.Password = content
		}
	}
	return value, validateCredentials(value)
}

func showCredentials(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("commander-api credentials", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	raw := flags.Bool("raw", false, "Print only the password")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander-api credentials [--raw]")
		fmt.Fprintln(stderr, "Print the running container's API credentials, or credentials from the current environment. Does not start the API.")
		fmt.Fprintln(stderr, "--raw prints only the password.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errors.New("invalid credential options; run commander-api credentials --help")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	value, err := lookupCredentials()
	if err != nil {
		return err
	}
	if *raw {
		_, err = fmt.Fprintln(stdout, value.Password)
		return err
	}
	_, err = fmt.Fprintf(stdout, "Username: %s\nPassword: %s\n", value.Username, value.Password)
	return err
}
