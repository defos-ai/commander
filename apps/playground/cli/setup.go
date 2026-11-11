package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/host"
)

type setupResult struct {
	Name           string `json:"name"`
	Transport      string `json:"transport"`
	URL            string `json:"url"`
	Authentication struct {
		Type   string `json:"type"`
		EnvVar string `json:"env_var"`
	} `json:"authentication"`
	ConnectionVerified    bool `json:"connection_verified"`
	TokenEnvironmentReady bool `json:"token_environment_ready"`
	ToolCount             int  `json:"tool_count"`
}

func showSetup(ctx context.Context, args []string, output, stderr io.Writer) error {
	flags := flag.NewFlagSet("commander setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("state-dir", env("COMMANDER_STATE_DIR", ""), "Local Commander profile; defaults to ~/.commander")
	endpoint := flags.String("mcp-url", env("COMMANDER_MCP_URL", ""), "Remote MCP endpoint; omitted discovers local Commander")
	asJSON := flags.Bool("json", false, "Print verified MCP connection details as JSON without credentials")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commander setup [--json] [--state-dir directory] [--mcp-url URL]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments; run commander setup --help")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	provided := false
	flags.Visit(func(f *flag.Flag) { provided = provided || f.Name == "mcp-url" })
	if provided && *endpoint == "" {
		return errors.New("--mcp-url must not be empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	address, credentials, err := setupConnection(ctx, *directory, *endpoint)
	if err != nil {
		return err
	}
	verification, err := mcp.Verify(ctx, address, credentials)
	if err != nil {
		return err
	}
	result := setupResult{Name: "commander", Transport: "streamable-http", URL: address, ConnectionVerified: true, ToolCount: verification.ToolCount,
		TokenEnvironmentReady: os.Getenv("COMMANDER_MCP_TOKEN") == credentials.Token}
	result.Authentication.Type = "bearer"
	result.Authentication.EnvVar = "COMMANDER_MCP_TOKEN"
	return printSetup(result, *asJSON, output)
}

func setupConnection(ctx context.Context, directory, endpoint string) (string, host.BearerCredentials, error) {
	if endpoint != "" {
		credentials := host.BearerCredentials{Token: os.Getenv("COMMANDER_MCP_TOKEN")}
		if err := credentials.Validate(); err != nil {
			return "", credentials, errors.New("set COMMANDER_MCP_TOKEN for the selected remote MCP endpoint")
		}
		return endpoint, credentials, nil
	}
	resolved, err := stateDirectory(directory)
	if err != nil {
		return "", host.BearerCredentials{}, err
	}
	instance, err := local.Discover(ctx, filepath.Join(resolved, "local"))
	if errors.Is(err, local.ErrNotRunning) {
		return "", host.BearerCredentials{}, errors.New("local Commander is not running; run commander start with the same --state-dir, then rerun setup. If your coding-agent shell stops background processes when a tool command ends, keep commander --all-in-one --headless running in a long-lived tool session and rerun setup from another tool call, using the same --state-dir")
	}
	if err != nil {
		return "", host.BearerCredentials{}, err
	}
	_, credentials, err := local.SavedCredentials(filepath.Join(resolved, "local"))
	return instance.MCPURL, credentials, err
}

func printSetup(result setupResult, asJSON bool, output io.Writer) error {
	if asJSON {
		return json.NewEncoder(output).Encode(result)
	}
	text := fmt.Sprintf("Commander MCP connection\nTransport: %s\nMCP: %s\nAuthentication: Bearer token (COMMANDER_MCP_TOKEN)\nConnection verified: %d tools and a read-only list_agents call\n", result.Transport, result.URL, result.ToolCount)
	if !result.TokenEnvironmentReady {
		text += "Provide this endpoint's token through your MCP client's authentication settings. For local credentials, use commander credentials mcp --raw with the same --state-dir.\n"
	}
	text += "Have your coding agent configure its own MCP client with these details and preserve existing settings. After configuring or reloading that client, call list_missions through it to verify tool execution. This command only reads and verifies the server connection; it does not confirm that your client can call the tools.\n"
	_, err := io.WriteString(output, text)
	return err
}
