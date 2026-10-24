package ui

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	admincli "github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
)

type adminCommandState struct {
	running bool
	cancel  context.CancelFunc
	done    <-chan struct{}
}

type adminCommandResult struct {
	args    []string
	text    string
	address string
	err     error
}

// SetAdminURL adds non-secret prefill data only from the configured API.
func (c *Config) SetAdminURL(address string) {
	c.AdminURL = address
	if c.API.URL != "" && client.ValidateURL(c.API.URL) == nil {
		query := url.Values{"api": {c.API.URL}, "username": {c.API.Credentials.Username}}
		c.AdminURL = strings.TrimRight(address, "/") + "/#/connect?" + query.Encode()
	}
	c.AdminHint = ""
}

func (m *Model) adminCommand(value string) (tea.Cmd, bool) {
	if fields := strings.Fields(value); len(fields) == 0 || fields[0] != "/admin" {
		return nil, false
	}
	m.input.Reset()
	args, err := adminArguments(strings.TrimSpace(strings.TrimPrefix(value, "/admin")))
	if err != nil {
		m.add("notice", err.Error())
		return nil, true
	}
	if len(args) == 0 {
		if m.config.AdminURL != "" {
			m.add("admin", "Admin:")
		} else if m.config.AdminHint != "" {
			m.add("notice", m.config.AdminHint)
		} else {
			m.add("notice", "Start Admin with /admin serve.")
		}
		return nil, true
	}
	if args[0] != "help" && args[0] != "missions" && args[0] != "secrets" && args[0] != "sessions" && args[0] != "serve" && !strings.HasPrefix(args[0], "-") {
		m.add("notice", "Usage: /admin [help|serve|missions|secrets|sessions]")
		return nil, true
	}
	// The session has one API connection. Never pair its retained password with
	// another destination supplied as a per-command flag.
	for _, argument := range args {
		if argument == "--" {
			break
		}
		name, _, _ := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if strings.HasPrefix(argument, "-") && (name == "api-url" || name == "api-username" || name == "api-password") {
			m.add("notice", "Use Playground's --api-url or its local API login form to configure the connection.")
			return nil, true
		}
	}
	return m.runAdmin(args), true
}

func (m *Model) runAdmin(args []string) tea.Cmd {
	if m.admin.running {
		m.add("notice", "An Admin command is running. Ctrl+C cancels it.")
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	results := make(chan adminCommandResult, 1)
	m.admin = adminCommandState{running: true, cancel: cancel, done: done}
	connection := client.Connection{URL: m.config.API.URL, Credentials: m.config.API.Credentials}
	start := m.config.ServeAdmin
	args = append([]string(nil), args...)
	go func() {
		defer close(done)
		result := adminCommandResult{args: args}
		if args[0] == "serve" && !adminServeHelp(args[1:]) {
			if start == nil {
				result.err = errors.New("Admin UI cannot be started in this session")
			} else {
				result.address, result.err = start(ctx, args[1:])
			}
		} else {
			var output bytes.Buffer
			result.err = admincli.RunWithOptions(ctx, args, nil, &output, &output, admincli.Options{Connection: &connection})
			result.text = output.String()
		}
		results <- result
	}()
	return func() tea.Msg { return <-results }
}

func (m *Model) adminResult(message tea.Msg) (tea.Cmd, bool) {
	result, ok := message.(adminCommandResult)
	if !ok {
		return nil, false
	}
	m.admin.cancel()
	m.admin = adminCommandState{}
	if text := strings.TrimSpace(result.text); text != "" {
		m.add("notice", text)
	}
	var input *admincli.SecretInputRequired
	if errors.As(result.err, &input) {
		return m.openAdminSecret(input.Key), true
	}
	var connection *client.ConnectionError
	if errors.As(result.err, &connection) || errors.Is(result.err, client.ErrAuthentication) {
		if errors.Is(result.err, client.ErrAuthentication) {
			m.config.API.Credentials.Password = ""
		}
		return m.connectAdminAPI(result.err.Error(), func() tea.Cmd { return m.runAdmin(result.args) }), true
	}
	if result.err != nil {
		m.add("notice", "Admin: "+result.err.Error())
	} else if result.address != "" {
		m.config.SetAdminURL(result.address)
		for i := range m.entries {
			if m.entries[i].kind == "admin" {
				m.entries[i].dirty = true
			}
		}
		m.add("admin", "Admin:")
	}
	return nil, true
}

// Check help using the UI flag grammar so a directory literally named --help
// cannot accidentally launch a second, unmanaged server through the CLI path.
func adminServeHelp(args []string) bool {
	for i := 0; i < len(args); i++ {
		name, _, equals := strings.Cut(args[i], "=")
		switch name {
		case "-h", "-help", "--help":
			return true
		case "--listen-address", "-listen-address", "--static-dir", "-static-dir", "--public-origin", "-public-origin", "--api-upstreams", "-api-upstreams":
			if !equals {
				i++
			}
		default:
			return false
		}
	}
	return false
}

// Quoting groups local paths and names; this never expands or executes a shell.
func adminArguments(value string) ([]string, error) {
	var args []string
	var token strings.Builder
	var quote rune
	started := false
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		if char == '\\' && quote != '\'' && i+1 < len(runes) {
			next := runes[i+1]
			if next == '"' || quote == 0 && (next == '\'' || unicode.IsSpace(next)) {
				token.WriteRune(next)
				i++
				started = true
				continue
			}
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				token.WriteRune(char)
			}
		} else if char == '\'' || char == '"' {
			quote, started = char, true
		} else if unicode.IsSpace(char) {
			if started {
				args = append(args, token.String())
				token.Reset()
				started = false
			}
		} else {
			token.WriteRune(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, errors.New("Admin command has an unclosed quote")
	}
	if started {
		args = append(args, token.String())
	}
	return args, nil
}
