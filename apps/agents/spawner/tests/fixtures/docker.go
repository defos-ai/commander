package fixtures

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

const (
	DockerImage             = "ghcr.io/defos-ai/fixture-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	DockerBuilderReference  = "github.com/defos-ai/commander/apps/agents/builder@v0.1.0"
	DockerImpostorReference = "github.com/other/builder-agent@v0.1.0"
)

type DockerManager struct {
	Manager *docker.Manager
	Config  docker.Config
	Engine  *fakes.DockerEngine
	Bus     *fscomms.FsBus
}

func NewDockerManager(t *testing.T) DockerManager {
	t.Helper()
	root := t.TempDir()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	engine := fakes.NewDockerEngine(DockerImage)
	engine.SetOnStart(func(ctx context.Context, current docker.Container) error {
		return PublishDockerRegistration(ctx, bus, current, nil)
	})
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		agentType := "fixture-agent"
		switch request.URL.String() {
		case FixtureReleaseURL + "agent.yaml", FixtureModuleReleaseURL + "agent.yaml":
		case "https://github.com/defos-ai/commander/releases/download/apps%2Fagents%2Fbuilder%2Fv0.1.0/agent.yaml",
			"https://github.com/other/builder-agent/releases/download/v0.1.0/agent.yaml":
			agentType = "builder-agent"
		default:
			return Response(request, http.StatusNotFound, "not found"), nil
		}
		manifest := "schema_version: 1\ntype: " + agentType + "\nversion: v0.1.0\ndocker:\n  image: " + DockerImage + "\n"
		return Response(request, http.StatusOK, manifest), nil
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	config := docker.Config{
		Bus:    bus,
		BusURL: "amqp://fixture/", StorageURL: "s3://fixture", Network: "fixture-network",
		Engine: engine, Resolver: resolver, RegistrationTimeout: time.Second,
		Environment: map[string]string{"AWS_REGION": "fixture-region", "AWS_SECRET_ACCESS_KEY": "private-storage-sentinel"},
	}
	manager, err := docker.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return DockerManager{Manager: manager, Config: config, Engine: engine, Bus: bus}
}

func PublishDockerRegistration(ctx context.Context, bus *fscomms.FsBus, current docker.Container, change func(*agent.AgentSnapshot)) error {
	agentID := strings.TrimPrefix(current.Name, "commander-")
	agentType, valid := id.RuntimeType(agentID, id.Agent)
	if !valid {
		return errors.New("fixture container has an invalid assigned agent ID")
	}
	snapshot := agent.AgentSnapshot{
		ID: agentID, Type: agent.AgentType(agentType + "-agent"), Status: agent.AgentReady,
		Server:  agent.ServerInfo{Hostname: "fixture", IPAddress: "127.0.0.1"},
		Actions: []actionmodel.Action{{Name: "fixture-action", Status: actionmodel.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
	}
	if change != nil {
		change(&snapshot)
	}
	message, err := messages.NewMessage(messages.AgentHeartbeat, snapshot.ID, snapshot)
	if err != nil {
		return err
	}
	return bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func RestartDockerManager(t *testing.T, fixture DockerManager) *docker.Manager {
	t.Helper()
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := docker.NewManager(t.Context(), fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

// A running container sends heartbeats independently of the spawner's lifetime.
func DockerHeartbeats(t *testing.T, fixture DockerManager, name string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			current, err := fixture.Engine.Inspect(ctx, name)
			if err == nil {
				err = PublishDockerRegistration(ctx, fixture.Bus, current, nil)
			}
			if err != nil {
				done <- err
				return
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}
}
