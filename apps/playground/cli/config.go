package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"commander/apps/playground/internal/llm"
	"commander/apps/playground/internal/ui"
)

type config struct {
	ui.Config
	AllInOne bool
}

func configuration(args []string, stderr io.Writer) (config, error) {
	var c config
	flags := flag.NewFlagSet("commander", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.MCPURL, "mcp-url", env("COMMANDER_MCP_URL", "http://localhost:4273/mcp"), "Commander MCP endpoint (COMMANDER_MCP_URL)")
	flags.BoolVar(&c.AllInOne, "all-in-one", false, "Start one local MCP, controller and agent; temporary state is removed on exit")
	flags.StringVar(&c.Provider, "provider", env("COMMANDER_LLM_PROVIDER", ""), "Skip provider selection: openai, anthropic, gemini, xai, deepseek, other (COMMANDER_LLM_PROVIDER)")
	flags.StringVar(&c.Model, "model", env("COMMANDER_LLM_MODEL", ""), "Model name; omitted asks interactively (COMMANDER_LLM_MODEL)")
	flags.StringVar(&c.BaseURL, "base-url", env("COMMANDER_LLM_BASE_URL", ""), "Override the model API base URL (COMMANDER_LLM_BASE_URL)")
	flags.BoolVar(&c.Debug, "debug", false, "Show redacted model API error details (COMMANDER_DEBUG)")
	flags.BoolVar(&c.NoColor, "no-color", os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb", "Disable corporate colors (NO_COLOR)")
	flags.Usage = func() {
		fmt.Fprint(stderr, "Usage: commander [flags]\n\nChat with a model and run missions through Commander MCP.\nUse --all-in-one for a local session, or connect to docker compose up --build -d.\n\nIn chat: /help, /operations, /watch, /answer, /cancel, /exit.\n\n")
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
