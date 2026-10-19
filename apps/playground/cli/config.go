package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/playground/internal/llm"
	"github.com/defos-ai/commander/apps/playground/internal/ui"
)

type config struct {
	ui.Config
	AllInOne    bool
	NoAdmin     bool
	SpawnAgents []string
}

func configuration(args []string, stderr io.Writer) (config, error) {
	var c config
	flags := flag.NewFlagSet("commander", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.MCPURL, "mcp-url", env("COMMANDER_MCP_URL", "http://localhost:4273/mcp"), "Commander MCP endpoint (COMMANDER_MCP_URL)")
	flags.BoolVar(&c.AllInOne, "all-in-one", false, "Start local MCP, API, Admin UI, controller and spawner; credentials are generated when unset; temporary state is removed on exit")
	flags.BoolVar(&c.NoAdmin, "no-admin", false, "Disable Admin UI in --all-in-one mode")
	flags.Func("spawn-agents", "Optional all-in-one startup package; repeat to start multiple agents; no packages start by default; an empty value clears the list", func(value string) error {
		if value == "" {
			c.SpawnAgents = []string{}
			return nil
		}
		if _, err := packages.ParseReference(value); err != nil {
			return err
		}
		c.SpawnAgents = append(c.SpawnAgents, value)
		return nil
	})
	flags.StringVar(&c.AdminURL, "admin-url", env("COMMANDER_ADMIN_URL", ""), "Override the browser-facing Admin UI URL; Docker all-in-one defaults to http://localhost:4275 (COMMANDER_ADMIN_URL)")
	flags.StringVar(&c.Provider, "provider", env("COMMANDER_LLM_PROVIDER", ""), "Skip provider selection: openai, anthropic, gemini, xai, deepseek, other (COMMANDER_LLM_PROVIDER)")
	flags.StringVar(&c.Model, "model", env("COMMANDER_LLM_MODEL", ""), "Model name; omitted asks interactively (COMMANDER_LLM_MODEL)")
	flags.StringVar(&c.BaseURL, "base-url", env("COMMANDER_LLM_BASE_URL", ""), "Override the model API base URL (COMMANDER_LLM_BASE_URL)")
	flags.BoolVar(&c.Debug, "debug", false, "Show redacted model API error details (COMMANDER_DEBUG)")
	flags.BoolVar(&c.NoColor, "no-color", os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb", "Disable corporate colors (NO_COLOR)")
	flags.Usage = func() {
		fmt.Fprint(stderr, "Usage: commander [flags]\n\nChat with a model and run missions through Commander MCP.\nUse --all-in-one for a local session, or connect to docker compose up --build -d.\n\nIn chat: /help, /admin, /operations, /watch, /cancel, /exit.\nAnswer questions with arrow keys and Enter, or type your answer.\n\n")
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
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "debug" {
			debugProvided = true
		}
		if f.Name == "mcp-url" {
			mcpProvided = true
		}
	})
	if c.AllInOne && mcpProvided {
		return c, errors.New("--all-in-one starts its own MCP; omit --mcp-url to use it")
	}
	if c.NoAdmin && !c.AllInOne {
		return c, errors.New("--no-admin requires --all-in-one")
	}
	if c.SpawnAgents != nil && !c.AllInOne {
		return c, errors.New("--spawn-agents requires --all-in-one")
	}
	if c.NoAdmin && c.AdminURL != "" {
		return c, errors.New("--no-admin cannot be combined with --admin-url or COMMANDER_ADMIN_URL")
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
		c.MCPCredentials.Token = os.Getenv("COMMANDER_MCP_TOKEN")
		if c.MCPCredentials.Token != "" {
			if err := c.MCPCredentials.Validate(); err != nil {
				return c, fmt.Errorf("COMMANDER_MCP_TOKEN: %w", err)
			}
		}
		if err := llm.ValidateURL(c.MCPURL); err != nil {
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
