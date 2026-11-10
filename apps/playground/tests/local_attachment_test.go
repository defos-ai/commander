package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestPlaygroundAttachesToLocalCommanderAndLeavesItsHostsRunning(t *testing.T) {
	fixtures.HeadlessEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	modelURL, model := fixtures.StartAttachmentModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend := fixtures.StartHeadless(t, directory)
	endpoints, err := backend.AwaitReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := local.Discover(ctx, filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}
	api, mcp, err := local.SavedCredentials(filepath.Join(directory, "local"))
	if err != nil {
		t.Fatal(err)
	}

	client := fixtures.StartLocalPlayground(t, directory, modelURL)
	if err := client.Await(ctx, "API key (optional"); err != nil {
		t.Fatal(err)
	}
	if err := client.Send(ctx, "\r"); err != nil {
		t.Fatal(err)
	}
	if err := client.Await(ctx, "Commander connected"); err != nil {
		t.Fatal(err)
	}
	if owner, err := state.LockProfile(directory); err == nil {
		_ = owner.Close()
		t.Fatal("attached Playground did not own the client profile")
	}
	if err := client.Send(ctx, "/credentials\r"); err != nil {
		t.Fatal(err)
	}
	if err := client.Await(ctx, "Local credentials are available only with --all-in-one."); err != nil {
		t.Fatal(err)
	}
	const prompt = "Hello from the attached Playground"
	if err := client.Send(ctx, prompt+"\r"); err != nil {
		t.Fatal(err)
	}
	if err := client.Await(ctx, fakes.AttachmentReply); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}

	status, retained := headlessStatus(t, ctx, directory)
	if status != "running" || retained.ID != instance.ID {
		t.Fatal("closing the attached client stopped or replaced its independent backend")
	}
	commander, err := bridge.Connect(ctx, endpoints.MCP, mcp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = commander.Close() })
	result, err := commander.Call(ctx, "list_agents", json.RawMessage(`{}`))
	if err != nil || result.IsError || !bytes.Contains(result.Data, []byte(`"type":"spawner-agent"`)) {
		t.Fatalf("attached client shutdown stopped backend work: %v", err)
	}
	requests := model.Requests()
	if len(requests) != 1 || !bytes.Contains(requests[0], []byte(prompt)) || !bytes.Contains(requests[0], []byte(`"name":"list_agents"`)) {
		t.Fatal("plain Playground did not use the authenticated local MCP tool inventory")
	}
	for _, secret := range []string{api.Password, mcp.Token} {
		if strings.Contains(client.Output.String()+client.Errors.String()+string(requests[0]), secret) {
			t.Fatal("attachment exposed retained backend credentials to terminal or model context")
		}
	}
	profile, err := state.Open(directory)
	if err != nil {
		t.Fatalf("client shutdown did not release Playground ownership: %v", err)
	}
	t.Cleanup(func() { _ = profile.Close() })
	history, err := profile.LoadHistory("local")
	if err != nil || !bytes.Contains(history, []byte(prompt)) || !bytes.Contains(history, []byte(fakes.AttachmentReply)) {
		t.Fatal("attached chat was not retained under the stable local history scope")
	}
	settings, err := profile.Load()
	if err != nil || settings.Remote.MCPURL != "" || settings.Remote.APIURL != "" || settings.Remote.APIUsername != "" {
		t.Fatal("attachment saved ephemeral local listeners as remote defaults")
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	probe := &fakes.InstanceProbe{}
	probe.Respond("unrelated-service", instance.ID, "")
	explicit := fixtures.StartInstanceProbe(t, probe)

	remoteClient := fixtures.StartLocalPlayground(t, directory, modelURL, "--mcp-url="+explicit.URL+"/mcp")
	if err := remoteClient.Await(ctx, "API key (optional"); err != nil {
		t.Fatal(err)
	}
	if err := remoteClient.Send(ctx, "\r"); err != nil {
		t.Fatal(err)
	}
	if err := remoteClient.Await(ctx, "cannot connect to Commander MCP"); err != nil {
		t.Fatal(err)
	}
	if err := remoteClient.Stop(); err != nil {
		t.Fatal(err)
	}

	if len(probe.Requests()) == 0 {
		t.Fatal("explicit MCP selection was replaced by local auto-attachment")
	}
	for _, request := range probe.Requests() {
		if request.Authorization {
			t.Fatal("explicit MCP destination received local bootstrap credentials")
		}
	}
}
