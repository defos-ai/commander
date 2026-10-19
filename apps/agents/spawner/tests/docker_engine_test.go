package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const engineImage = "ghcr.io/defos-ai/example-agent@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testDockerEngine(t *testing.T, handler transportFunc) docker.Engine {
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

func dockerPing(request *http.Request) *http.Response {
	value := response(request, http.StatusOK, "OK")
	value.Header.Set("API-Version", "1.48")
	value.Header.Set("OSType", "linux")
	return value
}

func inspectFixture() container.InspectResponse {
	init := true
	return container.InspectResponse{
		ID: "fixture123", Name: "/example-agent-a1b2c3d4", Platform: "linux",
		Config: &container.Config{
			Image: engineImage, Env: []string{"COMMANDER_AGENT_ID_SUFFIX=a1b2c3d4", "BUS_URL=amqp://rabbitmq"},
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

func jsonDockerResponse(t *testing.T, request *http.Request, status int, value any) *http.Response {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result := response(request, status, string(data))
	result.Header.Set("Content-Type", "application/json")
	return result
}

func TestDockerEngineNegotiatesCreatesAndStartsConstrainedContainer(t *testing.T) {
	var requests []string
	var created container.CreateRequest
	fixture := inspectFixture()
	engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.URL.Path == "/_ping" {
			return dockerPing(request), nil
		}
		if !strings.HasPrefix(request.URL.Path, "/v1.48/") {
			t.Fatalf("request did not use negotiated API version: %s", request.URL)
		}
		switch request.URL.Path {
		case "/v1.48/images/create":
			if request.Header.Get("X-Registry-Auth") != "" || request.Header.Get("Authorization") != "" {
				t.Fatal("pull forwards registry credentials")
			}
			if request.URL.Query().Get("fromImage") != "ghcr.io/defos-ai/example-agent" || request.URL.Query().Get("tag") != strings.Split(engineImage, "@")[1] {
				t.Fatal("image pull lost its digest pin")
			}
			return response(request, http.StatusOK, "{\"status\":\"pulling\"}\n{\"status\":\"done\"}\n"), nil
		case "/v1.48/networks/commander_default":
			return response(request, http.StatusOK, `{"Name":"commander_default","Id":"network123","Driver":"bridge"}`), nil
		case "/v1.48/containers/create":
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if request.URL.Query().Get("name") != "example-agent-a1b2c3d4" {
				t.Fatal("container name was not preserved")
			}
			return response(request, http.StatusCreated, `{"Id":"fixture123"}`), nil
		case "/v1.48/containers/fixture123/json":
			return jsonDockerResponse(t, request, http.StatusOK, fixture), nil
		case "/v1.48/containers/fixture123/start":
			return response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Docker API call: %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	})
	ctx := context.Background()
	if err := engine.Pull(ctx, engineImage); err != nil {
		t.Fatal(err)
	}
	value, err := engine.Create(ctx, docker.Spec{
		Name: "example-agent-a1b2c3d4", Image: engineImage, Network: "commander_default",
		Environment: map[string]string{"BUS_URL": "amqp://rabbitmq", "COMMANDER_AGENT_ID_SUFFIX": "a1b2c3d4"},
		Labels:      fixture.Config.Labels,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !value.Safe || value.ID != "fixture123" || value.Image != engineImage || value.Status != "created" {
		t.Fatalf("unexpected inspected container: %#v", value)
	}
	if created.Config.Image != engineImage || len(created.Config.Entrypoint) != 0 || len(created.Config.Cmd) != 0 ||
		!reflect.DeepEqual(created.Config.Env, []string{"BUS_URL=amqp://rabbitmq", "COMMANDER_AGENT_ID_SUFFIX=a1b2c3d4"}) {
		t.Fatalf("unexpected image command or bootstrap configuration: %#v", created.Config)
	}
	host := created.HostConfig
	if host.Privileged || len(host.CapAdd) != 0 || !reflect.DeepEqual(host.CapDrop, []string{"ALL"}) ||
		!reflect.DeepEqual(host.SecurityOpt, []string{"no-new-privileges"}) || host.AutoRemove || host.RestartPolicy.Name != "no" ||
		len(host.Binds) != 0 || len(host.Mounts) != 0 || len(host.PortBindings) != 0 || host.Init == nil || !*host.Init ||
		host.NetworkMode != "commander_default" || len(created.NetworkingConfig.EndpointsConfig) != 1 {
		t.Fatalf("unsafe launch configuration: %#v", host)
	}
	if err := engine.Start(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if requests[0] != "HEAD /_ping" || len(requests) != 6 {
		t.Fatalf("unexpected API calls: %v", requests)
	}
}

func TestDockerEngineRejectsUnsafeInspectedConfiguration(t *testing.T) {
	for name, change := range map[string]func(*container.InspectResponse){
		"privileged":              func(v *container.InspectResponse) { v.HostConfig.Privileged = true },
		"capabilities":            func(v *container.InspectResponse) { v.HostConfig.CapAdd = []string{"SYS_ADMIN"} },
		"missing capability drop": func(v *container.InspectResponse) { v.HostConfig.CapDrop = nil },
		"security override": func(v *container.InspectResponse) {
			v.HostConfig.SecurityOpt = append(v.HostConfig.SecurityOpt, "seccomp=unconfined")
		},
		"bind mount": func(v *container.InspectResponse) {
			v.HostConfig.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock"}
		},
		"image volume":    func(v *container.InspectResponse) { v.Mounts = []container.MountPoint{{Destination: "/data"}} },
		"host pid":        func(v *container.InspectResponse) { v.HostConfig.PidMode = "host" },
		"host ipc":        func(v *container.InspectResponse) { v.HostConfig.IpcMode = "host" },
		"host network":    func(v *container.InspectResponse) { v.HostConfig.NetworkMode = "host" },
		"extra network":   func(v *container.InspectResponse) { v.NetworkSettings.Networks["other"] = &network.EndpointSettings{} },
		"published ports": func(v *container.InspectResponse) { v.HostConfig.PublishAllPorts = true },
		"restart":         func(v *container.InspectResponse) { v.HostConfig.RestartPolicy.Name = "always" },
		"auto remove":     func(v *container.InspectResponse) { v.HostConfig.AutoRemove = true },
		"non Linux":       func(v *container.InspectResponse) { v.Platform = "windows" },
		"no host config":  func(v *container.InspectResponse) { v.HostConfig = nil },
		"duplicate environment": func(v *container.InspectResponse) {
			v.Config.Env = append(v.Config.Env, "COMMANDER_AGENT_ID_SUFFIX=ffffffff")
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := inspectFixture()
			change(&fixture)
			engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/_ping" {
					return dockerPing(request), nil
				}
				return jsonDockerResponse(t, request, http.StatusOK, fixture), nil
			})
			value, err := engine.Inspect(context.Background(), "fixture123")
			if err != nil || value.Safe {
				t.Fatalf("unsafe container accepted: safe=%t, error=%v", value.Safe, err)
			}
		})
	}
}

func TestDockerEngineRejectsBuiltinAndMissingNetworks(t *testing.T) {
	for _, name := range []string{"", "host", "none", "bridge", "default", "missing", "host-driver"} {
		t.Run(name, func(t *testing.T) {
			engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/_ping" {
					return dockerPing(request), nil
				}
				if request.URL.Path == "/v1.48/networks/missing" {
					return response(request, http.StatusNotFound, `{"message":"missing"}`), nil
				}
				if request.URL.Path == "/v1.48/networks/host-driver" {
					return response(request, http.StatusOK, `{"Name":"host-driver","Driver":"host"}`), nil
				}
				t.Fatalf("invalid network reached create: %s", request.URL.Path)
				return nil, errors.New("unexpected request")
			})
			_, err := engine.Create(context.Background(), docker.Spec{Name: "example-agent-a1b2c3d4", Image: engineImage, Network: name})
			if err == nil {
				t.Fatal("invalid network accepted")
			}
		})
	}
}

func TestDockerEngineSanitizesPullAndDaemonErrors(t *testing.T) {
	for _, body := range []string{
		`{"errorDetail":{"message":"registry-token-do-not-log","code":403},"error":"registry-token-do-not-log"}`,
		`{"status":"incomplete`,
	} {
		engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/_ping" {
				return dockerPing(request), nil
			}
			return response(request, http.StatusOK, body), nil
		})
		if err := engine.Pull(context.Background(), engineImage); err == nil || strings.Contains(err.Error(), "registry-token") {
			t.Fatalf("pull error absent or not sanitized: %v", err)
		}
	}
	engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/_ping" {
			return dockerPing(request), nil
		}
		return response(request, http.StatusNotFound, `{"message":"secret-bootstrap-value"}`), nil
	})
	if _, err := engine.Inspect(context.Background(), "unknown"); !errors.Is(err, docker.ErrNotFound) || strings.Contains(err.Error(), "secret-bootstrap") {
		t.Fatalf("missing container error absent or not sanitized: %v", err)
	}
}

func TestDockerEnginePreservesCancellationWithoutLeakingTransportDetails(t *testing.T) {
	engine := testDockerEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/_ping" {
			return dockerPing(request), nil
		}
		return nil, fmt.Errorf("secret-bootstrap-value: %w", context.Canceled)
	})
	err := engine.Start(context.Background(), "fixture123")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret-bootstrap") {
		t.Fatalf("cancellation not preserved safely: %v", err)
	}
}
