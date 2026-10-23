package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
	"github.com/defos-ai/commander/framework/host"
)

type config struct {
	ui.Config
	NoAdmin bool
}

func (c *config) configureLocalSession(session *local.Session) {
	c.MCP = ui.Connection[host.BearerCredentials]{
		URL:         session.MCPURL,
		Credentials: session.MCPCredentials,
	}
	c.API = ui.Connection[host.BasicCredentials]{
		URL:         session.APIURL,
		Credentials: session.APICredentials,
	}
	c.LocalAgentCount = session.AgentCount
	c.Notices = session.Notices
	if c.Debug {
		c.Notices = append(c.Notices, "Local host logs: "+session.Directory+"/hosts.log (removed on exit)")
	}
	c.configureLocalAdmin(session)
}

func configuration(args []string, stderr io.Writer) (config, error) {
	var c config
	flags := flag.NewFlagSet("commander", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.MCP.URL, "mcp-url", env("COMMANDER_MCP_URL", "http://localhost:4273/mcp"), "Commander MCP endpoint (COMMANDER_MCP_URL)")
	flags.StringVar(&c.API.URL, "api-url", env("COMMANDER_API_URL", ""), "Commander API URL for supplying agent secrets; omitted asks when needed (COMMANDER_API_URL)")
	flags.BoolVar(&c.AllInOne, "all-in-one", false, "Start local MCP, API, Admin UI, controller, builder and spawner; credentials are generated when unset; temporary state is removed on exit")
	flags.BoolVar(&c.NoAdmin, "no-admin", false, "Disable Admin UI in --all-in-one mode")
	flags.StringVar(&c.AdminURL, "admin-url", env("COMMANDER_ADMIN_URL", ""), "Admin UI browser address; defaults to http://localhost:4275 or the native all-in-one address (COMMANDER_ADMIN_URL)")
	flags.StringVar(&c.Provider, "provider", env("COMMANDER_LLM_PROVIDER", ""), "Skip provider selection: openai, anthropic, gemini, xai, deepseek, other (COMMANDER_LLM_PROVIDER)")
	flags.StringVar(&c.Model, "model", env("COMMANDER_LLM_MODEL", ""), "Model name; omitted asks interactively (COMMANDER_LLM_MODEL)")
	flags.StringVar(&c.BaseURL, "base-url", env("COMMANDER_LLM_BASE_URL", ""), "Override the model API base URL (COMMANDER_LLM_BASE_URL)")
	flags.BoolVar(&c.Debug, "debug", false, "Show redacted model API error details (COMMANDER_DEBUG)")
	flags.BoolVar(&c.NoColor, "no-color", os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb", "Disable corporate colors (NO_COLOR)")
	flags.Usage = func() {
		fmt.Fprint(stderr, "Usage: commander [flags]\n\nChat with a model and run missions through Commander MCP.\nUse --all-in-one for a local session, or connect to docker compose up --build -d.\n\nIn chat: /help, /admin, /secrets, /operations, /watch, /cancel, /exit.\nAnswer questions with arrow keys and Enter, or type your answer.\n\n")
		fmt.Fprintln(stderr, "Paste the MCP token when prompted, or set COMMANDER_MCP_TOKEN. Use /credentials inside all-in-one Playground. commander credentials api|mcp [--raw] reads credentials from the current environment or the running all-in-one container.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return c, err
	}
	if flags.NArg() != 0 {
		return c, errors.New("unexpected positional arguments; run commander --help")
	}
	debugProvided := false
	mcpProvided := false
	apiProvided := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "debug" {
			debugProvided = true
		}
		if f.Name == "mcp-url" {
			mcpProvided = true
		}
		if f.Name == "api-url" {
			apiProvided = true
		}
	})
	if c.AllInOne && mcpProvided {
		return c, errors.New("--all-in-one starts its own MCP; omit --mcp-url to use it")
	}
	if c.AllInOne && apiProvided {
		return c, errors.New("--all-in-one starts its own API; omit --api-url to use it")
	}
	if c.NoAdmin && !c.AllInOne {
		return c, errors.New("--no-admin requires --all-in-one")
	}
	if c.NoAdmin && c.AdminURL != "" {
		return c, errors.New("--no-admin cannot be combined with --admin-url or COMMANDER_ADMIN_URL")
	}
	if !c.AllInOne && c.AdminURL == "" {
		c.AdminURL = defaultAdminURL
	}
	if c.AdminURL != "" {
		if err := validateAdminURL(c.AdminURL); err != nil {
			return c, err
		}
	}
	c.AdminHint = "Set --admin-url or COMMANDER_ADMIN_URL to your Admin UI's browser address."
	if value := env("COMMANDER_DEBUG", ""); !debugProvided && value != "" {
		var err error
		c.Debug, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("COMMANDER_DEBUG must be a boolean (true or false)")
		}
	}
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	known := c.Provider == ""
	c.Keys = map[string]string{}
	for _, preset := range llm.Presets() {
		if preset.ID == c.Provider {
			known = true
		}
		for _, name := range preset.KeyEnv {
			if key := strings.TrimSpace(os.Getenv(name)); key != "" {
				c.Keys[preset.ID] = key
				break
			}
		}
	}
	if !known {
		return c, errors.New("unknown provider; run commander --help")
	}
	if !c.AllInOne {
		username := env("COMMANDER_API_USERNAME", "commander")
		if username == "" {
			username = "commander"
		}
		c.API.Credentials = host.BasicCredentials{Username: username, Password: os.Getenv("COMMANDER_API_PASSWORD")}
		if c.API.URL != "" {
			if err := client.ValidateURL(c.API.URL); err != nil {
				return c, fmt.Errorf("api-url: %w", err)
			}
		}
		c.MCP.Credentials.Token = os.Getenv("COMMANDER_MCP_TOKEN")
		if c.MCP.Credentials.Token != "" {
			if err := c.MCP.Credentials.Validate(); err != nil {
				return c, fmt.Errorf("COMMANDER_MCP_TOKEN: %w", err)
			}
		}
		if err := llm.ValidateURL(c.MCP.URL); err != nil {
			return c, fmt.Errorf("mcp-url: %w", err)
		}
	}
	if c.BaseURL != "" {
		if err := llm.ValidateURL(c.BaseURL); err != nil {
			return c, fmt.Errorf("base-url: %w", err)
		}
	}
	return c, nil
}
func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}
