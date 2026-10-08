package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
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

func resolveCredentials(supplied *host.Credentials) (host.Credentials, error) {
	value := host.Credentials{Username: os.Getenv("API_USERNAME"), Password: os.Getenv("API_PASSWORD")}
	if supplied != nil {
		value = *supplied
	}
	return value, validateCredentials(value)
}

func validateCredentials(value host.Credentials) error {
	err := value.Validate()
	if errors.Is(err, host.ErrMissingCredentials) {
		return errors.New(missingCredentials)
	}
	if err != nil {
		return fmt.Errorf("API_USERNAME/API_PASSWORD: %w", err)
	}
	return nil
}

func processCredentials(pid int) (host.Credentials, error) {
	if runtime.GOOS != "linux" {
		return host.Credentials{}, errors.New("--pid requires Linux; use it inside the API container")
	}
	environment, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return host.Credentials{}, fmt.Errorf("read API process environment: %w", err)
	}
	var value host.Credentials
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
	flags.SetOutput(stderr)
	var pid int
	flags.IntVar(&pid, "pid", 0, "Read credentials from a running Linux process; use 1 inside the Compose API container")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander-api credentials [--pid PID]")
		fmt.Fprintln(stderr, "Print API_USERNAME and API_PASSWORD from the environment to stdout. Does not start the API.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	fromProcess := false
	flags.Visit(func(value *flag.Flag) { fromProcess = value.Name == "pid" })
	var value host.Credentials
	var err error
	if fromProcess {
		if pid <= 0 {
			return errors.New("--pid must be a positive process ID")
		}
		value, err = processCredentials(pid)
	} else {
		value, err = resolveCredentials(nil)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Username: %s\nPassword: %s\n", value.Username, value.Password)
	return err
}
