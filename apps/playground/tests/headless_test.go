package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adminclient "github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestHeadlessAllInOneServesAuthenticatedToolsAndRetainsStateAcrossRestart(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	clientData := fixtures.HeadlessProfileWithClientData(t, directory)
	t.Setenv("COMMANDER_LLM_PROVIDER", "unusable-client-provider")
	t.Setenv("COMMANDER_LLM_BASE_URL", "not-a-model-endpoint")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	interactive := cli.Run(ctx, []string{"--all-in-one", "--state-dir", directory, "--provider=openai", "--base-url=http://127.0.0.1:1"}, nil, io.Discard, io.Discard)
	if interactive == nil || !strings.Contains(interactive.Error(), "saved Playground settings are invalid") {
		t.Fatalf("normal Playground no longer validates retained client state: %v", interactive)
	}

	app := fixtures.StartHeadless(t, directory)
	endpoints, err := app.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	apiCredentials, mcpCredentials, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(directory, "local", "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	commander, err := bridge.Connect(ctx, endpoints.MCP, mcpCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commander.Close() })
	result, err := commander.Call(ctx, "list_agents", json.RawMessage(`{}`))
	if err != nil || result.IsError || !bytes.Contains(result.Data, []byte(`"type":"spawner-agent"`)) {
		t.Fatalf("headless MCP did not expose the ready spawner: %v", err)
	}
	api, err := adminclient.New(adminclient.Connection{URL: endpoints.API, Credentials: apiCredentials})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	if _, err := api.Request(ctx, http.MethodGet, "/api/v1/missions/build-agent", nil); err != nil {
		t.Fatalf("bundled mission was not available at readiness: %v", err)
	}
	if _, err := api.Request(ctx, http.MethodPost, "/api/v1/missions", []byte(fixtures.HeadlessMission)); err != nil {
		t.Fatal(err)
	}
	duplicate := cli.Run(ctx, []string{"--all-in-one", "--headless", "--state-dir", directory}, nil, io.Discard, io.Discard)
	if duplicate == nil || !strings.Contains(duplicate.Error(), "already in use") {
		t.Fatalf("a second controller was not rejected by profile ownership: %v", duplicate)
	}
	if app.Input.Reads() != 0 || strings.Contains(app.Output.String(), "\x1b") {
		t.Fatal("headless startup read terminal input or rendered terminal controls")
	}
	for _, private := range []string{apiCredentials.Password, mcpCredentials.Token, string(key)} {
		if strings.Contains(app.Output.String()+app.Errors.String(), private) {
			t.Fatal("headless output exposed bootstrap credentials")
		}
	}
	_ = commander.Close()
	api.Close()
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	assertHeadlessStopped(t, endpoints)

	restarted := fixtures.StartHeadless(t, directory)
	ready, err := restarted.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	savedAPI, savedMCP, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil || savedAPI != apiCredentials || savedMCP != mcpCredentials {
		t.Fatal("headless restart replaced saved credentials")
	}
	savedKey, err := os.ReadFile(filepath.Join(directory, "local", "secrets.key"))
	if err != nil || !bytes.Equal(savedKey, key) {
		t.Fatal("headless restart replaced the original encryption key")
	}
	api, err = adminclient.New(adminclient.Connection{URL: ready.API, Credentials: savedAPI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Request(ctx, http.MethodGet, "/api/v1/missions/headless-retained", nil); err != nil {
		t.Fatalf("headless restart lost saved API state: %v", err)
	}
	api.Close()
	if err := restarted.Stop(); err != nil {
		t.Fatal(err)
	}
	assertHeadlessStopped(t, ready)
	for name, expected := range clientData {
		actual, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("headless startup changed retained client file %s", name)
		}
	}
	profile, err := state.LockProfile(directory)
	if err != nil {
		t.Fatalf("shutdown retained profile ownership: %v", err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessTemporarySessionIgnoresProfileAndCleansItsRuntime(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	temporary := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporary)
	}
	directory := filepath.Join(t.TempDir(), "unfamiliar-profile")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	app := fixtures.StartHeadless(t, directory, "--temporary")
	endpoints, err := app.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if app.Input.Reads() != 0 {
		t.Fatal("headless temporary startup read terminal input")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("temporary startup created a persistent profile")
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "commander-playground-") {
		t.Fatal("temporary session did not own exactly one disposable runtime directory")
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}

	assertHeadlessStopped(t, endpoints)
	entries, err = os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatal("headless shutdown left its temporary runtime behind")
	}
}

func TestHeadlessRejectsInvalidStartupWithoutReplacingRetainedFiles(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	clientData := fixtures.HeadlessProfileWithClientData(t, directory)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app := fixtures.StartHeadless(t, directory)
	if _, err := app.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(directory, "local", "secrets.key")
	brokenKey := []byte("invalid-retained-key")
	if err := os.WriteFile(keyPath, brokenKey, 0600); err != nil {
		t.Fatal(err)
	}

	err := cli.Run(ctx, []string{"--all-in-one", "--headless", "--state-dir", directory}, nil, io.Discard, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "invalid local encryption key") {
		t.Fatalf("corrupt bootstrap was not rejected: %v", err)
	}
	actual, readErr := os.ReadFile(keyPath)
	if readErr != nil || !bytes.Equal(actual, brokenKey) {
		t.Fatal("startup replaced a corrupt retained encryption key")
	}
	for name, expected := range clientData {
		actual, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("failed startup changed retained client file %s", name)
		}
	}
	profile, err := state.LockProfile(directory)
	if err != nil {
		t.Fatalf("failed startup retained profile ownership: %v", err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cli.Run(ctx, []string{"--headless", "--temporary"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "--all-in-one") {
		t.Fatalf("headless remote mode was not rejected: %v", err)
	}
}

func assertHeadlessStopped(t *testing.T, endpoints fixtures.HeadlessEndpoints) {
	t.Helper()
	for _, endpoint := range []string{endpoints.API, endpoints.MCP} {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		connection, err := net.DialTimeout("tcp", parsed.Host, time.Second)
		if err == nil {
			connection.Close()
			t.Fatal("headless shutdown returned with a service still listening")
		}
	}
}
