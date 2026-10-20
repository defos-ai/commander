package release_test

import (
	"slices"
	"strings"
	"testing"
)

func TestCIComposeDockerSpawner(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Profiles    []string
			Command     []string
			Environment map[string]string
			Volumes     []string
			DependsOn   map[string]struct{ Condition string } `yaml:"depends_on"`
		}
	}
	readCIYAML(t, "../..", "compose.yaml", &compose)
	spawner, ok := compose.Services["spawner-agent"]
	if !ok || len(spawner.Profiles) != 0 {
		t.Fatal("Docker spawner must start with the default Compose services")
	}
	if len(spawner.Command) != 0 {
		t.Fatal("Compose spawner must start without automatic package launches")
	}
	if compose.Services["playground"].DependsOn["spawner-agent"].Condition != "service_started" {
		t.Fatal("Playground must start after the spawner")
	}
	builder, ok := compose.Services["builder-agent"]
	if !ok || len(builder.Profiles) != 0 {
		t.Fatal("Builder must start with the default Compose services")
	}
	if compose.Services["playground"].DependsOn["builder-agent"].Condition != "service_started" {
		t.Fatal("Playground must start after the builder")
	}
	for _, name := range []string{"browser-agent", "web-agent-1", "web-agent-2"} {
		if _, exists := compose.Services[name]; exists {
			t.Errorf("%s must be launched by the spawner instead of Compose", name)
		}
	}
	if spawner.Environment["SPAWNER_RUNTIME"] != "docker" || spawner.Environment["SPAWNER_DOCKER_NETWORK"] == "" {
		t.Fatal("Compose spawner must use Docker and an explicitly selected network")
	}
	if !slices.Contains(spawner.Volumes, "spawner-state:/var/lib/commander/spawner") || spawner.Environment["SPAWNER_STATE_DIR"] != "/var/lib/commander/spawner" {
		t.Fatal("Compose spawner must retain launch records across host recreation")
	}
	if !slices.Contains(spawner.Volumes, "/var/run/docker.sock:/var/run/docker.sock") {
		t.Fatal("Compose Docker spawner must have access to the host daemon")
	}
	for name, service := range compose.Services {
		for _, mount := range service.Volumes {
			if strings.Contains(mount, "docker.sock") && name != "spawner-agent" {
				t.Errorf("service %s must not receive Docker daemon access", name)
			}
		}
	}
	for _, name := range []string{"DATABASE_URL", "API_PASSWORD", "MCP_TOKEN", "SECRETS_ENCRYPTION_KEY"} {
		if _, exists := spawner.Environment[name]; exists {
			t.Errorf("spawner must not receive %s", name)
		}
	}
}
