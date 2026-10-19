package release_test

import (
	"slices"
	"strings"
	"testing"
)

func TestComposeDockerSpawnerIsOptIn(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Profiles    []string
			Environment map[string]string
			Volumes     []string
		}
	}
	readCIYAML(t, "../..", "compose.yaml", &compose)
	spawner, ok := compose.Services["spawner-agent"]
	if !ok || !slices.Contains(spawner.Profiles, "spawner") {
		t.Fatal("Docker spawner must require the opt-in spawner profile")
	}
	if spawner.Environment["SPAWNER_RUNTIME"] != "docker" || spawner.Environment["SPAWNER_DOCKER_NETWORK"] == "" {
		t.Fatal("Compose spawner must use Docker and an explicitly selected network")
	}
	if !slices.Contains(spawner.Volumes, "spawner-state:/var/lib/commander/spawner") || spawner.Environment["SPAWNER_STATE_DIR"] != "/var/lib/commander/spawner" {
		t.Fatal("Compose spawner must retain launch records across host recreation")
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
