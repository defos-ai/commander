package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	"github.com/defos-ai/commander/framework/id"
)

func TestBackgroundCommanderStartsOnceAndStopsWithoutLosingSavedWork(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	adminDocument := fixtures.BackgroundAdminAssets(t)
	t.Setenv("COMMANDER_TEMPORARY", "true")
	t.Setenv("PORT", "1")
	address := fixtures.FixedMCPAddress(t)
	directory := fixtures.BackgroundProfile(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	instance := startBackgroundCommander(t, ctx, directory)

	if instance.PID == os.Getpid() || instance.PID < 1 || instance.ID == "" {
		t.Fatal("start did not launch a distinct background process")
	}
	if instance.MCPURL != "http://"+address+"/mcp" {
		t.Fatal("background startup did not honor the configured MCP address")
	}
	if status, discovered := headlessStatus(t, ctx, directory); status != "running" || discovered != instance {
		t.Fatal("the background process was not discoverable after start returned")
	}
	assertBackgroundAdminServes(t, ctx, instance.AdminURL, adminDocument)
	apiCredentials, mcpCredentials, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(directory, "local", "secrets.key"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := bridge.Connect(ctx, instance.MCPURL, mcpCredentials)
	if err != nil {
		t.Fatal(err)
	}
	result, callErr := client.Call(ctx, "list_agents", json.RawMessage(`{}`))
	_ = client.Close()
	if callErr != nil || result.IsError || !bytes.Contains(result.Data, []byte(`"type":"spawner-agent"`)) {
		t.Fatalf("background MCP did not expose its authenticated ready spawner: %v", callErr)
	}
	api, err := adminclient.New(adminclient.Connection{URL: instance.APIURL, Credentials: apiCredentials})
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Request(ctx, http.MethodPost, "/api/v1/missions", []byte(fixtures.HeadlessMission))
	api.Close()
	if err != nil {
		t.Fatal(err)
	}

	if repeated := startBackgroundCommander(t, ctx, directory); repeated != instance {
		t.Fatal("repeated start replaced a running backend")
	}
	if err := cli.Run(ctx, []string{"stop", "--state-dir", directory}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	if status, _ := headlessStatus(t, ctx, directory); status != "stopped" {
		t.Fatal("stop returned with a live background instance")
	}
	assertHeadlessStopped(t, fixtures.HeadlessEndpoints{API: instance.APIURL, MCP: instance.MCPURL, Admin: instance.AdminURL})
	if err := cli.Run(ctx, []string{"stop", "--state-dir", directory}, nil, io.Discard, io.Discard); err != nil {
		t.Fatalf("repeated stop failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "local", "stop-"+instance.ID), nil, 0600); err != nil {
		t.Fatal(err)
	}

	restarted := startBackgroundCommander(t, ctx, directory)

	if restarted.ID == instance.ID {
		t.Fatal("restart reused the previous process identity")
	}
	if restarted.MCPURL != instance.MCPURL {
		t.Fatal("restart changed the configured MCP endpoint")
	}
	assertBackgroundAdminServes(t, ctx, restarted.AdminURL, adminDocument)
	savedAPI, savedMCP, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil || savedAPI != apiCredentials || savedMCP != mcpCredentials {
		t.Fatal("background restart replaced saved credentials")
	}
	if _, err := bridge.Verify(ctx, instance.MCPURL, mcpCredentials); err != nil {
		t.Fatalf("the original MCP URL and credentials no longer worked after restart: %v", err)
	}
	savedKey, err := os.ReadFile(filepath.Join(directory, "local", "secrets.key"))
	if err != nil || !bytes.Equal(savedKey, key) {
		t.Fatal("background restart replaced the original encryption key")
	}
	api, err = adminclient.New(adminclient.Connection{URL: restarted.APIURL, Credentials: savedAPI})
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Request(ctx, http.MethodGet, "/api/v1/missions/headless-retained", nil)
	api.Close()
	if err != nil {
		t.Fatalf("background restart lost saved API state: %v", err)
	}
}

func TestBackgroundCommanderKeepsRequiredHostsRunningWhenAdminFails(t *testing.T) {
	for _, failure := range []string{"missing assets", "occupied address"} {
		t.Run(failure, func(t *testing.T) {
			fixtures.HeadlessEnvironment(t)
			if failure == "occupied address" {
				fixtures.BackgroundAdminAssets(t)
				fixtures.OccupyHostAddress(t, "UI_LISTEN_ADDRESS")
			}
			directory := fixtures.BackgroundProfile(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var output, diagnostic bytes.Buffer

			err := cli.Run(ctx, []string{"start", "--json", "--state-dir", directory}, nil, &output, &diagnostic)

			if err != nil {
				t.Fatalf("unavailable Admin prevented background startup: %v; %s", err, diagnostic.String())
			}
			var instance local.Instance
			if err := json.Unmarshal(output.Bytes(), &instance); err != nil {
				t.Fatalf("start did not return machine-readable readiness: %v", err)
			}
			if instance.AdminURL != "" {
				t.Fatal("start advertised an unavailable Admin UI")
			}
			if !strings.Contains(diagnostic.String(), "Admin UI unavailable; Commander is running.") {
				t.Fatalf("start did not report the optional Admin failure: %s", diagnostic.String())
			}
			if status, discovered := headlessStatus(t, ctx, directory); status != "running" || discovered != instance {
				t.Fatal("unavailable Admin left required hosts undiscoverable")
			}
			apiCredentials, mcpCredentials, err := local.SavedCredentials(filepath.Join(directory, "local"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := bridge.Verify(ctx, instance.MCPURL, mcpCredentials); err != nil {
				t.Fatalf("unavailable Admin stopped MCP: %v", err)
			}
			api, err := adminclient.New(adminclient.Connection{URL: instance.APIURL, Credentials: apiCredentials})
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.Request(ctx, http.MethodGet, "/api/v1/missions", nil); err != nil {
				t.Fatalf("unavailable Admin stopped the API: %v", err)
			}
		})
	}
}

func TestBackgroundStartupFailureDoesNotReportReadinessOrHoldState(t *testing.T) {
	for _, service := range []string{"API", "MCP"} {
		t.Run(service, func(t *testing.T) {
			fixtures.HeadlessEnvironment(t)
			directory := fixtures.BackgroundProfile(t)
			fixtures.OccupyHostAddress(t, service+"_LISTEN_ADDRESS")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var output bytes.Buffer

			err := cli.Run(ctx, []string{"start", "--json", "--state-dir", directory}, nil, &output, io.Discard)

			if err == nil || output.Len() != 0 {
				t.Fatalf("occupied %s address did not prevent ready startup: %v", service, err)
			}
			if status, _ := headlessStatus(t, ctx, directory); status != "stopped" {
				t.Fatal("failed startup left a discoverable backend")
			}
			root, err := os.OpenRoot(filepath.Join(directory, "local"))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			lease, err := state.LockFile(root, ".lock")
			if err != nil {
				t.Fatalf("failed startup retained backend ownership: %v", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStopWaitsForBackendOwnershipAfterDiscoveryIsWithdrawn(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "local")
	instance := local.Instance{Version: 1, ID: id.New(), PID: os.Getpid(), StartedAt: time.Now().UTC(), APIURL: "http://127.0.0.1:1", MCPURL: "http://127.0.0.1:1/mcp"}
	fixtures.WriteLocalInstance(t, directory, instance)
	lease := fixtures.HoldBackendLease(t, directory)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)

	go func() {
		_, err := local.Stop(ctx, directory)
		done <- err
	}()
	if err := fixtures.AwaitShutdownRequest(ctx, directory, instance.ID); err != nil {
		t.Fatalf("stop did not submit an instance-scoped shutdown request: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "instance.json")); err != nil {
		t.Fatal(err)
	}
	var stopErr error
	select {
	case stopErr = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop ignored its cancellation deadline while backend cleanup was still pending")
	}

	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("stop considered withdrawn discovery sufficient while cleanup still held ownership: %v", stopErr)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if stopped, err := local.Stop(context.Background(), directory); err != nil || stopped {
		t.Fatalf("an already released backend was not treated as stopped: %v", err)
	}
}

func TestStopPreservesStaleRecordsWithoutSignalingTheirPID(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "local")
	instance := local.Instance{Version: 1, ID: id.New(), PID: os.Getpid(), StartedAt: time.Now().UTC(), APIURL: "http://127.0.0.1:1", MCPURL: "http://127.0.0.1:1/mcp"}
	fixtures.WriteLocalInstance(t, directory, instance)
	lease := fixtures.HoldBackendLease(t, directory)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(directory, "instance.json")
	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stopped, err := local.Stop(ctx, directory)

	if err != nil || stopped {
		t.Fatalf("a stale descriptor was treated as an owned live process: %v", err)
	}
	after, err := os.ReadFile(recordPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("stop rewrote a crash-left instance descriptor")
	}
	if _, err := os.Stat(filepath.Join(directory, "stop-"+instance.ID)); !os.IsNotExist(err) {
		t.Fatal("stop wrote a shutdown request without a live backend owner")
	}
}

func startBackgroundCommander(t *testing.T, ctx context.Context, directory string) local.Instance {
	t.Helper()
	var output bytes.Buffer
	var diagnostic bytes.Buffer
	if err := cli.Run(ctx, []string{"start", "--json", "--state-dir", directory}, nil, &output, &diagnostic); err != nil {
		t.Fatalf("start background Commander: %v; %s", err, diagnostic.String())
	}
	var status struct {
		Status string `json:"status"`
		local.Instance
	}
	if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.Status != "running" {
		t.Fatalf("start did not return machine-readable readiness: %v", err)
	}
	if strings.Contains(output.String(), "password") || strings.Contains(output.String(), "token") || strings.Contains(output.String(), "secrets") {
		t.Fatal("start output exposed bootstrap credential fields")
	}
	return status.Instance
}

func assertBackgroundAdminServes(t *testing.T, ctx context.Context, endpoint, document string) {
	t.Helper()
	if endpoint == "" {
		t.Fatal("background startup did not advertise its ready Admin UI")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("background Admin was not reachable: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != document {
		t.Fatalf("background Admin did not serve its configured assets: status=%d, error=%v", response.StatusCode, err)
	}
}
