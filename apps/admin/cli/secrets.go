package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

const maxSecretInputBytes = 64 << 10

type SecretInputRequired struct{ Key secrets.Key }

func (*SecretInputRequired) Error() string { return "provide a secret value on stdin or in a terminal" }

func runSecrets(ctx context.Context, configuration config, stdin io.Reader, stdout, stderr io.Writer) error {
	args := configuration.arguments[1:]
	if len(args) == 0 || len(args) == 1 && args[0] == "help" {
		return secretHelp(stdout)
	}
	command := args[0]
	if !validSecretArguments(command, args, configuration) {
		return &UsageError{cause: errors.New("invalid secret command or arguments; run commander-admin secrets --help")}
	}
	var key secrets.Key
	if len(args) > 1 {
		key.Scope.AgentType = args[1]
		if err := key.Scope.Validate(); err != nil {
			return &UsageError{cause: err}
		}
	}
	if command == "set" || command == "delete" {
		key.Name = args[2]
		if err := key.Validate(); err != nil {
			return &UsageError{cause: err}
		}
	}
	api, err := client.New(configuration.connection)
	if err != nil {
		return &UsageError{cause: err}
	}
	defer api.Close()
	switch command {
	case "list":
		items, err := adminsecrets.List(ctx, api, key.Scope.AgentType)
		if err != nil {
			return err
		}
		if configuration.json {
			return writeJSON(stdout, struct {
				Items []adminsecrets.Metadata `json:"items"`
			}{items})
		}
		for _, item := range items {
			if _, err := fmt.Fprintf(stdout, "%s · %s · v%d\n", item.Name, item.Status, item.Version); err != nil {
				return err
			}
		}
		if len(items) == 0 {
			_, err = fmt.Fprintln(stdout, "No saved secrets.")
		}
		return err
	case "requests":
		items, err := adminsecrets.Requests(ctx, api, key.Scope.AgentType)
		if err != nil {
			return err
		}
		if configuration.json {
			return writeJSON(stdout, struct {
				Items []adminsecrets.Request `json:"items"`
			}{items})
		}
		for _, item := range items {
			if _, err := fmt.Fprintf(stdout, "%s · %s · %s\n", item.AgentType, item.Name, item.Status); err != nil {
				return err
			}
		}
		if len(items) == 0 {
			_, err = fmt.Fprintln(stdout, "No pending secret requests.")
		}
		return err
	}
	metadata, err := adminsecrets.Get(ctx, api, key)
	if err != nil {
		return err
	}
	if command == "delete" {
		if metadata == nil {
			return errors.New("secret not found")
		}
		if err := adminsecrets.Delete(ctx, api, key, *metadata); err != nil {
			return err
		}
		if configuration.json {
			return writeJSON(stdout, struct {
				AgentType string `json:"agent_type"`
				Name      string `json:"name"`
				Deleted   bool   `json:"deleted"`
			}{key.Scope.AgentType, key.Name, true})
		}
		_, err := fmt.Fprintf(stdout, "Deleted %s · %s.\n", key.Scope.AgentType, key.Name)
		return err
	}
	if stdin == nil {
		return &UsageError{cause: &SecretInputRequired{Key: key}}
	}
	value, err := readSecret(ctx, stdin, stderr)
	if err != nil {
		return err
	}
	defer clear(value)
	saved, err := adminsecrets.Save(ctx, api, key, string(value), metadata)
	if err != nil {
		return err
	}
	if configuration.json {
		return writeJSON(stdout, saved)
	}
	_, err = fmt.Fprintf(stdout, "Saved %s · %s · v%d. Waiting for agent readiness.\n", saved.AgentType, saved.Name, saved.Version)
	return err
}

func validSecretArguments(command string, args []string, configuration config) bool {
	if configuration.dryRun {
		return false
	}
	switch command {
	case "list":
		return len(args) == 2
	case "requests":
		return len(args) == 1 || len(args) == 2
	case "set", "delete":
		return len(args) == 3
	default:
		return false
	}
}

func readSecret(ctx context.Context, stdin io.Reader, output io.Writer) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var value []byte
	var err error
	if stdin == nil {
		return nil, &UsageError{cause: errors.New("provide a secret value on stdin or in a terminal")}
	}
	file, isFile := stdin.(*os.File)
	terminal := isFile && term.IsTerminal(int(file.Fd()))
	input, finish, err := secretReader(ctx, stdin)
	if err != nil {
		return nil, err
	}
	defer finish()
	if terminal {
		value, err = promptSecret(file, input, output)
	} else {
		value, err = io.ReadAll(io.LimitReader(input, maxSecretInputBytes+1))
	}
	if err != nil {
		clear(value)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !terminal {
			return nil, errors.New("could not read the secret value from stdin")
		}
		return nil, err
	}
	if len(value) > maxSecretInputBytes || !utf8.Valid(value) {
		clear(value)
		return nil, &UsageError{cause: errors.New("secret value must be valid UTF-8 within 64 KiB")}
	}
	if err := ctx.Err(); err != nil {
		clear(value)
		return nil, err
	}
	return value, nil
}

func promptSecret(file *os.File, input io.Reader, output io.Writer) (value []byte, result error) {
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil, errors.New("could not open the masked secret prompt")
	}
	defer func() {
		if err := term.Restore(int(file.Fd()), state); err != nil {
			clear(value)
			value = nil
			result = errors.Join(result, errors.New("could not restore terminal input"))
		}
	}()
	counted := &secretTerminalReader{Reader: input}
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{counted, output}, "")
	text, err := terminal.ReadPassword("Secret value: ")
	if errors.Is(err, io.EOF) {
		return nil, context.Canceled
	}
	if err != nil {
		return nil, errors.New("could not read the secret value")
	}
	// x/term caps interactive lines at 4096 runes. Count raw bytes too so a
	// paste followed by editing cannot silently save a truncated value.
	if counted.bytes >= 4096 {
		return nil, &UsageError{cause: errors.New("interactive input is too long; pipe or redirect longer values")}
	}
	return []byte(text), nil
}

type secretTerminalReader struct {
	io.Reader
	bytes int
}

func (reader *secretTerminalReader) Read(buffer []byte) (int, error) {
	n, err := reader.Reader.Read(buffer)
	reader.bytes += n
	return n, err
}

// Cancel terminal and Unix pipe reads without closing caller-owned stdin.
// Other inputs use bounded direct reads; Windows cancelreader opens CONIN$ and
// must never replace redirected input with the console.
func secretReader(ctx context.Context, input io.Reader) (io.Reader, func(), error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, func() {}, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.New("could not inspect secret input")
	}
	terminal := term.IsTerminal(int(file.Fd()))
	if !terminal && (runtime.GOOS == "windows" || info.Mode()&os.ModeNamedPipe == 0) {
		return input, func() {}, nil
	}
	reader, err := cancelreader.NewReader(file)
	if err != nil {
		return nil, nil, errors.New("could not open secret input")
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		reader.Cancel()
		close(done)
	})
	finish := func() {
		if !stop() {
			<-done
		}
		reader.Close()
	}
	return reader, finish, nil
}

func secretHelp(output io.Writer) error {
	_, err := io.WriteString(output, `Usage: commander-admin secrets <command> [options]

Commands:
  list AGENT_TYPE              List saved secret metadata
  requests [AGENT_TYPE]        List pending request metadata
  set AGENT_TYPE NAME          Read piped input or open a masked terminal prompt
  delete AGENT_TYPE NAME       Delete the observed secret ID and version

Values are write-only. Use --json for metadata results; never pass values as arguments.
Piped or redirected input preserves exact bytes, including newlines (UTF-8, up to 64 KiB).
After an uncertain write, inspect metadata before submitting again.
Saving does not confirm agent readiness.
Options: --json, --api-url URL, --api-username NAME, -h, --help.
`)
	return err
}
