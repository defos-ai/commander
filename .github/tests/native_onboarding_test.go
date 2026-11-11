package release_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIInstallersNativeOnboarding(t *testing.T) {
	source, configured := os.LookupEnv("COMMANDER_TEST_NATIVE_BINARY")
	if !configured {
		t.Skip("set COMMANDER_TEST_NATIVE_BINARY to the native embed_ui Commander build")
	}
	if strings.TrimSpace(source) == "" {
		t.Fatal("COMMANDER_TEST_NATIVE_BINARY must point to the native embed_ui Commander build")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	installed := fixtures.InstallNativeCommander(t, source)
	assertNativeInstalledBytes(t, source, installed)
	commander := fixtures.NewNativeCommander(t, installed)

	started := commander.Status(t, "start")

	if started.Status != "running" || started.InstanceID == "" || started.PID < 1 || started.PID == os.Getpid() || started.APIURL == "" || started.MCPURL == "" {
		t.Fatalf("installed start did not return a ready background instance: %+v", started)
	}
	if status := commander.Status(t, "status"); status != started {
		t.Fatal("the installed executable could not rediscover its background instance")
	}
	assertNativeAdmin(t, commander, started.AdminURL)
	setup := commander.Run(t, "setup", "--json")
	var connection struct {
		URL                string `json:"url"`
		ConnectionVerified bool   `json:"connection_verified"`
		ToolCount          int    `json:"tool_count"`
	}
	if json.Unmarshal(setup, &connection) != nil || !connection.ConnectionVerified || connection.URL != started.MCPURL || connection.ToolCount == 0 {
		t.Fatal("installed setup did not verify the discoverable MCP endpoint")
	}
	token := strings.TrimSpace(string(commander.Run(t, "credentials", "mcp", "--raw")))
	if token == "" || bytes.Contains(setup, []byte(token)) || bytes.Contains(commander.Run(t, "status", "--json"), []byte(token)) {
		t.Fatal("local credential lookup was empty or public setup/status exposed its token")
	}
	client := fixtures.ConnectNativeMCP(t, connection.URL, token)
	assertNativeMissionListed(t, client, "install-agent")
	client.Call(t, "create_mission", map[string]any{
		"name": "native-retained", "objectives": []string{"Retain saved work across native restarts"},
		"input_schema": map[string]string{"type": "object"}, "output_schema": map[string]string{"type": "object"},
	})
	if repeated := commander.Status(t, "start"); repeated != started {
		t.Fatal("repeated start replaced the running native instance")
	}

	stopped := commander.Status(t, "stop")

	if stopped.Status != "stopped" || commander.Status(t, "status").Status != "stopped" {
		t.Fatal("native stop left the background instance discoverable")
	}
	restarted := commander.Status(t, "start")
	if restarted.Status != "running" || restarted.InstanceID == started.InstanceID || restarted.MCPURL != started.MCPURL {
		t.Fatal("native restart did not publish a new instance at the configured MCP endpoint")
	}
	if actual := strings.TrimSpace(string(commander.Run(t, "credentials", "mcp", "--raw"))); actual != token {
		t.Fatal("native restart replaced the saved MCP credential")
	}
	reconnected := fixtures.ConnectNativeMCP(t, started.MCPURL, token)
	assertNativeMissionListed(t, reconnected, "native-retained")
	assertNativeAdmin(t, commander, restarted.AdminURL)
}

func assertNativeInstalledBytes(t *testing.T, source, installed string) {
	t.Helper()
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(installed)
	if err != nil || sha256.Sum256(actual) != sha256.Sum256(original) {
		t.Fatal("installer did not retain the exact built native executable")
	}
}

func assertNativeMissionListed(t *testing.T, client *fixtures.NativeMCP, name string) {
	t.Helper()
	var result struct {
		Missions []struct{ Name string } `json:"missions"`
	}
	if err := json.Unmarshal(client.Call(t, "list_missions", map[string]any{}), &result); err != nil {
		t.Fatal(err)
	}
	for _, mission := range result.Missions {
		if mission.Name == name {
			return
		}
	}
	t.Fatalf("external MCP client could not list mission %s", name)
}

func assertNativeAdmin(t *testing.T, commander *fixtures.NativeCommander, endpoint string) {
	t.Helper()
	if endpoint == "" {
		t.Fatal("native executable did not advertise its bundled Admin UI")
	}
	status, document := commander.Get(t, endpoint)
	if status != http.StatusOK || !bytes.Contains(document, []byte("<title>COMMANDER | Administration</title>")) {
		t.Fatal("native executable did not serve bundled Admin HTML")
	}
	asset := regexp.MustCompile(`(?:src|href)="([^"]*/assets/[^"?]+\.(?:js|css))"`).FindSubmatch(document)
	if len(asset) != 2 {
		t.Fatal("bundled Admin HTML did not reference a built JavaScript or CSS asset")
	}
	address, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := url.Parse(string(asset[1]))
	if err != nil {
		t.Fatal(err)
	}
	status, data := commander.Get(t, address.ResolveReference(reference).String())
	if status != http.StatusOK || len(data) == 0 || bytes.Contains(data, []byte("<!doctype html>")) {
		t.Fatal("native executable did not serve the referenced Admin asset")
	}
}
