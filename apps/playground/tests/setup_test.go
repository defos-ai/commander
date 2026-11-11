package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestSetupVerifiesLocalCommanderWithoutClientSoftwareOrClientStateChanges(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	clientFiles := fixtures.HeadlessProfileWithClientData(t, directory)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var stoppedOutput bytes.Buffer

	err := cli.Run(ctx, []string{"setup", "--json", "--state-dir", directory}, nil, &stoppedOutput, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "commander start") || !strings.Contains(err.Error(), "commander --all-in-one --headless") || !strings.Contains(err.Error(), "same --state-dir") {
		t.Fatalf("stopped-instance setup did not explain background and foreground startup: %v", err)
	}
	if stoppedOutput.Len() != 0 {
		t.Fatal("stopped-instance setup reported a verified connection")
	}
	if _, err := os.Stat(filepath.Join(directory, "local")); !os.IsNotExist(err) {
		t.Fatal("setup created local backend state instead of waiting for an explicit launch")
	}

	app := fixtures.StartHeadless(t, directory)
	endpoints, err := app.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, credentials, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	connection := runSetup(t, ctx, directory)

	if connection.Name != "commander" || connection.Transport != "streamable-http" || connection.URL != endpoints.MCP || !connection.ConnectionVerified || connection.ToolCount < 2 {
		t.Fatal("setup did not verify the running local Commander's real MCP tool inventory")
	}
	if connection.Authentication.Type != "bearer" || connection.Authentication.EnvVar != "COMMANDER_MCP_TOKEN" || connection.TokenEnvironmentReady {
		t.Fatal("setup did not describe the external client's missing token environment accurately")
	}
	t.Setenv("COMMANDER_MCP_TOKEN", credentials.Token)

	repeated := runSetup(t, ctx, directory)

	if repeated.URL != endpoints.MCP || !repeated.ConnectionVerified || !repeated.TokenEnvironmentReady || repeated.ToolCount != connection.ToolCount {
		t.Fatal("repeated setup changed the connection or failed to observe the supplied token environment")
	}
	for name, expected := range clientFiles {
		actual, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("setup changed retained client file %s", name)
		}
	}
	var output bytes.Buffer
	if err := cli.Run(ctx, []string{"setup", "--state-dir", directory}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), credentials.Token) {
		t.Fatal("human-readable setup exposed a saved MCP token")
	}
	if !strings.Contains(output.String(), "call list_missions through it") {
		t.Fatal("setup did not guide verification through the calling MCP client")
	}
}

func TestSetupExplicitRemoteConnectionUsesOnlyItsProvidedToken(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	app := fixtures.StartHeadless(t, directory)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	endpoints, err := app.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, credentials, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}
	probe, endpoint := fixtures.StartSetupMCPProbe(t, credentials.Token, "explicit-remote-token")
	var output bytes.Buffer
	args := []string{"setup", "--json", "--state-dir", directory, "--mcp-url", endpoint}

	err = cli.Run(ctx, args, nil, &output, io.Discard)

	if err == nil || probe.RequestCount() != 0 {
		t.Fatal("explicit remote setup used local credentials without a remote token")
	}
	t.Setenv("COMMANDER_MCP_TOKEN", "explicit-remote-token")

	err = cli.Run(ctx, args, nil, &output, io.Discard)

	if err == nil || probe.RequestCount() == 0 || !probe.SawExplicitToken() || probe.SawLocalToken() {
		t.Fatal("remote authentication failure mixed local credentials or accepted an unverified endpoint")
	}
	if bytes.Contains(output.Bytes(), []byte(credentials.Token)) || strings.Contains(err.Error(), credentials.Token) {
		t.Fatal("remote setup exposed saved local credentials")
	}
	t.Setenv("COMMANDER_MCP_TOKEN", credentials.Token)
	unfamiliar := filepath.Join(t.TempDir(), "not-a-local-profile")

	connection := runSetup(t, ctx, unfamiliar, "--mcp-url", endpoints.MCP)

	if connection.URL != endpoints.MCP || !connection.ConnectionVerified || !connection.TokenEnvironmentReady || connection.ToolCount < 2 {
		t.Fatal("explicit remote setup did not verify authenticated Commander access")
	}
	if _, err := os.Stat(unfamiliar); !os.IsNotExist(err) {
		t.Fatal("explicit remote setup created or relied on a local profile")
	}
}

type setupConnectionResult struct {
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

func runSetup(t *testing.T, ctx context.Context, directory string, extra ...string) setupConnectionResult {
	t.Helper()
	var output bytes.Buffer
	args := append([]string{"setup", "--json", "--state-dir", directory}, extra...)
	if err := cli.Run(ctx, args, nil, &output, io.Discard); err != nil {
		t.Fatalf("set up MCP connection: %v", err)
	}
	var result setupConnectionResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("setup did not return JSON: %v", err)
	}
	if token := os.Getenv("COMMANDER_MCP_TOKEN"); token != "" && bytes.Contains(output.Bytes(), []byte(token)) {
		t.Fatal("setup JSON exposed a supplied MCP token")
	}
	return result
}
