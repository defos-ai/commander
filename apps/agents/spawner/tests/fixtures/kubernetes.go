package fixtures

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/kubernetes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

type KubernetesManager struct {
	Manager *kubernetes.Manager
	Config  kubernetes.ManagerConfig
	Client  *fakes.PodClient
	Bus     *fakes.TrackingBus
}

func NewKubernetesManager(t *testing.T) KubernetesManager {
	t.Helper()
	bus, err := fscomms.NewFsBus(filepath.Join(t.TempDir(), "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	trackedBus := &fakes.TrackingBus{Bus: bus}
	client := &fakes.PodClient{}
	client.SetOnRelease(func(ctx context.Context, pod kubernetes.Pod) error {
		return PublishKubernetesRegistration(ctx, trackedBus, pod, nil)
	})
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		agentType := "fixture-agent"
		switch request.URL.String() {
		case FixtureReleaseURL + "agent.yaml", FixtureModuleReleaseURL + "agent.yaml":
		case BuilderReleaseURL + "agent.yaml":
			agentType = "builder-agent"
		default:
			return Response(request, http.StatusNotFound, "not found"), nil
		}
		manifest := "schema_version: 1\ntype: " + agentType + "\nversion: v0.1.0\ndocker:\n  image: " + DockerImage + "\n"
		return Response(request, http.StatusOK, manifest), nil
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	config := kubernetes.ManagerConfig{Pod: PodConfig(), Bus: trackedBus, Client: client, Resolver: resolver,
		RegistrationTimeout: time.Second, PollInterval: 5 * time.Millisecond}
	manager, err := kubernetes.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return KubernetesManager{Manager: manager, Config: config, Client: client, Bus: trackedBus}
}

func PublishKubernetesRegistration(ctx context.Context, bus comms.Bus, pod kubernetes.Pod, edit func(*agent.AgentSnapshot)) error {
	message, err := KubernetesRegistration(pod, edit)
	if err != nil {
		return err
	}
	return bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func KubernetesRegistration(pod kubernetes.Pod, edit func(*agent.AgentSnapshot)) (messages.Message, error) {
	agentID := pod.Metadata.Labels["ai.defos.commander.agent-id"]
	agentType, valid := id.RuntimeType(agentID, id.Agent)
	if !valid {
		return messages.Message{}, errors.New("fixture Pod lacks an assigned agent ID")
	}
	snapshot := agent.AgentSnapshot{ID: agentID, Type: agent.AgentType(agentType + "-agent"), Status: agent.AgentReady,
		Server:  agent.ServerInfo{Hostname: pod.Metadata.Name, IPAddress: "127.0.0.1"},
		Actions: []actionmodel.Action{{Name: "fixture-action", Status: actionmodel.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}}}
	if edit != nil {
		edit(&snapshot)
	}
	return messages.NewMessage(messages.AgentHeartbeat, snapshot.ID, snapshot)
}

func RestartKubernetesManager(t *testing.T, fixture KubernetesManager) *kubernetes.Manager {
	t.Helper()
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := kubernetes.NewManager(t.Context(), fixture.Config)
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

func SpawnKubernetesAsync(t *testing.T, manager *kubernetes.Manager, parent context.Context, requestID string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	finished, stopped := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(stopped)
		_, err := manager.Spawn(ctx, requestID, Reference)
		finished <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("test launch did not end after cancellation")
		}
	})
	return finished
}
