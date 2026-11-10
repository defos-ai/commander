package tests

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/id"
)

func TestLocalDiscoveryRejectsUnverifiedInstancesWithoutSendingCredentials(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	localDirectory := filepath.Join(directory, "local")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if status, _ := headlessStatus(t, ctx, directory); status != "stopped" {
		t.Fatal("a nonexistent local instance was reported as running")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("reading status created a local profile")
	}
	probe := &fakes.InstanceProbe{}
	server := fixtures.StartInstanceProbe(t, probe)
	mcpProbe := &fakes.InstanceProbe{}
	mcpServer := fixtures.StartInstanceProbe(t, mcpProbe)
	instance := local.Instance{Version: 1, ID: id.New(), PID: os.Getpid(), StartedAt: time.Now().UTC(), APIURL: server.URL, MCPURL: mcpServer.URL + "/mcp"}
	probe.Respond("commander", instance.ID, "")
	mcpProbe.Respond("commander", instance.ID, "")
	fixtures.WriteLocalInstance(t, localDirectory, instance)

	if discovered, err := local.Discover(ctx, localDirectory); err != nil || discovered != instance {
		t.Fatalf("matching public API and MCP identities were not discoverable: %v", err)
	}
	mcpProbe.Respond("commander", id.New(), "")
	if _, err := local.Discover(ctx, localDirectory); !errors.Is(err, local.ErrNotRunning) {
		t.Fatalf("matching API identity concealed a different MCP instance: %v", err)
	}
	mcpProbe.Respond("commander", instance.ID, "")
	for _, response := range []struct{ application, identity string }{{"commander", id.New()}, {"unrelated-service", instance.ID}} {
		probe.Respond(response.application, response.identity, "")
		if _, err := local.Discover(ctx, localDirectory); !errors.Is(err, local.ErrNotRunning) {
			t.Fatalf("unverified public identity was accepted: %v", err)
		}
		if status, _ := headlessStatus(t, ctx, directory); status != "stopped" {
			t.Fatal("status trusted a stale or unrelated service")
		}
	}
	redirected := &fakes.InstanceProbe{}
	target := fixtures.StartInstanceProbe(t, redirected)
	probe.Respond("commander", instance.ID, target.URL+"/aboutz")

	if _, err := local.Discover(ctx, localDirectory); !errors.Is(err, local.ErrNotRunning) {
		t.Fatalf("redirected identity probe was accepted: %v", err)
	}
	if len(redirected.Requests()) != 0 {
		t.Fatal("local discovery followed a redirect to another endpoint")
	}
	before := len(probe.Requests())
	nonlocal := instance
	nonlocal.APIURL = "http://192.0.2.1:4274"
	fixtures.WriteLocalInstance(t, localDirectory, nonlocal)

	if _, err := local.Discover(ctx, localDirectory); err == nil || errors.Is(err, local.ErrNotRunning) {
		t.Fatalf("nonloopback instance record was not rejected: %v", err)
	}
	if len(probe.Requests()) != before {
		t.Fatal("local discovery probed a record containing a nonloopback endpoint")
	}
	fixtures.WriteLocalInstance(t, localDirectory, instance)
	server.Close()

	if _, err := local.Discover(ctx, localDirectory); !errors.Is(err, local.ErrNotRunning) {
		t.Fatalf("crash-left instance record was accepted without live hosts: %v", err)
	}
	if status, _ := headlessStatus(t, ctx, directory); status != "stopped" {
		t.Fatal("status treated a crash-left record as a running instance")
	}
	invalid := []byte(`{"version":1,"instance_id":"invalid"}`)
	if err := os.WriteFile(filepath.Join(localDirectory, "instance.json"), invalid, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer

	err := cli.Run(ctx, []string{"status", "--json", "--state-dir", directory}, nil, &output, io.Discard)

	if err == nil || errors.Is(err, local.ErrNotRunning) || output.Len() != 0 {
		t.Fatal("status concealed an invalid instance record as a normal stopped state")
	}
	for _, request := range append(probe.Requests(), mcpProbe.Requests()...) {
		if request.Authorization || request.Method != http.MethodGet || request.Path != "/aboutz" {
			t.Fatal("public identity discovery sent credentials or used an authenticated endpoint")
		}
	}
}
