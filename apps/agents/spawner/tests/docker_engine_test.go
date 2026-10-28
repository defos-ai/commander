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

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

func TestDockerEngineNegotiatesCreatesAndStartsConstrainedContainer(t *testing.T) {
	var requests []string
	var created container.CreateRequest
	fixture := fixtures.InspectContainer()
	engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Header.Get("Authorization") != "" || request.URL.Path != "/v1.48/images/create" && request.Header.Get("X-Registry-Auth") != "" {
			t.Fatal("registry authentication escaped the pull request")
		}
		if request.URL.Path == "/_ping" {
			return fixtures.DockerPing(request), nil
		}
		if !strings.HasPrefix(request.URL.Path, "/v1.48/") {
			t.Fatalf("request did not use negotiated API version: %s", request.URL)
		}
		switch request.URL.Path {
		case "/v1.48/images/create":
			auth, err := authconfig.Decode(request.Header.Get("X-Registry-Auth"))
			if err != nil || auth.Password != privateToken || auth.Username == "" || auth.ServerAddress != "ghcr.io" || auth.RegistryToken != "" || auth.IdentityToken != "" {
				t.Fatal("pull did not authenticate with the request's GitHub token")
			}
			if request.URL.Query().Get("fromImage") != "ghcr.io/defos-ai/example-agent" || request.URL.Query().Get("tag") != strings.Split(fixtures.EngineImage, "@")[1] {
				t.Fatal("image pull lost its digest pin")
			}
			return fixtures.Response(request, http.StatusOK, "{\"status\":\"pulling\"}\n{\"status\":\"done\"}\n"), nil
		case "/v1.48/networks/commander_default":
			return fixtures.Response(request, http.StatusOK, `{"Name":"commander_default","Id":"network123","Driver":"bridge"}`), nil
		case "/v1.48/containers/create":
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if request.URL.Query().Get("name") != "example-agent-a1b2c3d4" {
				t.Fatal("container name was not preserved")
			}
			return fixtures.Response(request, http.StatusCreated, `{"Id":"fixture123"}`), nil
		case "/v1.48/containers/fixture123/json":
			return fixtures.DockerResponse(t, request, http.StatusOK, fixture), nil
		case "/v1.48/containers/fixture123/start":
			return fixtures.Response(request, http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected Docker API call: %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	})
	ctx := packages.WithGitHubToken(t.Context(), []byte(privateToken))
	if err := engine.Pull(ctx, fixtures.EngineImage); err != nil {
		t.Fatal(err)
	}
	value, err := engine.Create(ctx, docker.Spec{
		Name: "example-agent-a1b2c3d4", Image: fixtures.EngineImage, Network: "commander_default",
		Environment: map[string]string{"BUS_URL": "amqp://rabbitmq", "COMMANDER_AGENT_ID_SUFFIX": "a1b2c3d4"},
		Labels:      fixture.Config.Labels,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !value.Safe || value.ID != "fixture123" || value.Image != fixtures.EngineImage || value.Status != "created" {
		t.Fatalf("unexpected inspected container: %#v", value)
	}
	if created.Config.Image != fixtures.EngineImage || len(created.Config.Entrypoint) != 0 || len(created.Config.Cmd) != 0 ||
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

func TestDockerEngineScopesGitHubAuthenticationToGHCR(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "host-token-must-not-be-read")
	private := packages.WithGitHubToken(t.Context(), []byte(privateToken))
	authenticated := false
	pulls := 0
	engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" {
			t.Fatal("registry token became daemon authorization")
		}
		if request.URL.Path == "/_ping" {
			if request.Header.Get("X-Registry-Auth") != "" {
				t.Fatal("registry token leaked to daemon negotiation")
			}
			return fixtures.DockerPing(request), nil
		}
		if request.URL.Path != "/v1.48/images/create" {
			t.Fatal("unexpected daemon request")
		}
		pulls++
		header := request.Header.Get("X-Registry-Auth")
		if authenticated {
			auth, err := authconfig.Decode(header)
			if err != nil || auth.Password != privateToken || auth.Username == "" || auth.ServerAddress != "ghcr.io" {
				t.Fatal("GHCR pull lacks scoped authentication")
			}
		} else if header != "" {
			t.Fatal("anonymous or other-registry pull received credentials")
		}
		return fixtures.Response(request, http.StatusOK, "{\"status\":\"done\"}\n"), nil
	})
	for _, tc := range []struct {
		name, registry string
		ctx            context.Context
		authenticated  bool
	}{
		{"private GHCR", "ghcr.io", private, true},
		{"subsequent public GHCR", "ghcr.io", t.Context(), false},
		{"masked token", "ghcr.io", packages.WithGitHubToken(private, nil), false},
		{"other registry", "registry.example", private, false},
		{"lookalike registry", "ghcr.io.example", private, false},
		{"explicit port", "ghcr.io:443", private, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authenticated = tc.authenticated
			image := strings.Replace(fixtures.EngineImage, "ghcr.io", tc.registry, 1)
			if err := engine.Pull(tc.ctx, image); err != nil {
				t.Fatal(err)
			}
		})
	}
	if pulls != 6 {
		t.Fatalf("expected six pulls, got %d", pulls)
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
			fixture := fixtures.InspectContainer()
			change(&fixture)
			engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/_ping" {
					return fixtures.DockerPing(request), nil
				}
				return fixtures.DockerResponse(t, request, http.StatusOK, fixture), nil
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
			engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/_ping" {
					return fixtures.DockerPing(request), nil
				}
				if request.URL.Path == "/v1.48/networks/missing" {
					return fixtures.Response(request, http.StatusNotFound, `{"message":"missing"}`), nil
				}
				if request.URL.Path == "/v1.48/networks/host-driver" {
					return fixtures.Response(request, http.StatusOK, `{"Name":"host-driver","Driver":"host"}`), nil
				}
				t.Fatalf("invalid network reached create: %s", request.URL.Path)
				return nil, errors.New("unexpected request")
			})
			_, err := engine.Create(context.Background(), docker.Spec{Name: "example-agent-a1b2c3d4", Image: fixtures.EngineImage, Network: name})
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
		engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/_ping" {
				return fixtures.DockerPing(request), nil
			}
			return fixtures.Response(request, http.StatusOK, body), nil
		})
		ctx := packages.WithGitHubToken(t.Context(), []byte("registry-token-do-not-log"))
		if err := engine.Pull(ctx, fixtures.EngineImage); err == nil || strings.Contains(err.Error(), "registry-token") {
			t.Fatalf("pull error absent or not sanitized: %v", err)
		}
	}
	engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/_ping" {
			return fixtures.DockerPing(request), nil
		}
		return fixtures.Response(request, http.StatusNotFound, `{"message":"secret-bootstrap-value"}`), nil
	})
	if _, err := engine.Inspect(context.Background(), "unknown"); !errors.Is(err, docker.ErrNotFound) || strings.Contains(err.Error(), "secret-bootstrap") {
		t.Fatalf("missing container error absent or not sanitized: %v", err)
	}
}

func TestDockerEnginePreservesCancellationWithoutLeakingTransportDetails(t *testing.T) {
	engine := fixtures.DockerEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/_ping" {
			return fixtures.DockerPing(request), nil
		}
		return nil, fmt.Errorf("secret-bootstrap-value: %w", context.Canceled)
	})
	err := engine.Start(context.Background(), "fixture123")
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret-bootstrap") {
		t.Fatalf("cancellation not preserved safely: %v", err)
	}
}
