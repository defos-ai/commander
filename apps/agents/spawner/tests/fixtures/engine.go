package fixtures

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const EngineImage = "ghcr.io/defos-ai/example-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func DockerEngine(t *testing.T, handler fakes.Transport) docker.Engine {
	t.Helper()
	value, err := client.New(client.WithHost("http://docker.invalid"), client.WithHTTPClient(&http.Client{Transport: handler}))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := docker.NewEngineWithClient(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	return engine
}

func DockerPing(request *http.Request) *http.Response {
	value := Response(request, http.StatusOK, "OK")
	value.Header.Set("API-Version", "1.48")
	value.Header.Set("OSType", "linux")
	return value
}

func InspectContainer() container.InspectResponse {
	init := true
	return container.InspectResponse{
		ID: "fixture123", Name: "/example-agent-a1b2c3d4", Platform: "linux",
		Config: &container.Config{
			Image: EngineImage, Env: []string{"COMMANDER_AGENT_ID_SUFFIX=a1b2c3d4", "BUS_URL=amqp://rabbitmq"},
			Labels: map[string]string{"commander.agent_id": "example-agent-a1b2c3d4"},
		},
		State: &container.State{Status: "created"},
		HostConfig: &container.HostConfig{
			NetworkMode: "commander_default", Init: &init, CapDrop: []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"}, RestartPolicy: container.RestartPolicy{Name: "no"},
			IpcMode: container.IPCModePrivate, CgroupnsMode: container.CgroupnsModePrivate,
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{"commander_default": {}}},
	}
}

func DockerResponse(t *testing.T, request *http.Request, status int, value any) *http.Response {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result := Response(request, status, string(data))
	result.Header.Set("Content-Type", "application/json")
	return result
}
