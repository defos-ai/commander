package fixtures

import (
	"strings"

	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func DependencyManifest() manifest.Manifest {
	return manifest.Manifest{SchemaVersion: 1, Type: "fixture-agent", Version: "v1.2.3",
		Native: map[string]manifest.NativeAsset{"linux/amd64": {
			Asset: "fixture-agent_linux_amd64", SHA256: strings.Repeat("a", 64),
		}},
		Docker: &manifest.Docker{Image: "ghcr.io/defos-ai/fixture-agent@sha256:" + strings.Repeat("b", 64)},
		Dependencies: []agents.Dependency{
			{Name: "lightpanda", Description: "Browser runtime: \"fast\" # 浏览器", Version: "1.0"},
			{Name: "helper", Description: "true"},
		},
	}
}

func RejectedDependencyManifestFields() []struct{ Name, YAML string } {
	return []struct{ Name, YAML string }{
		{"null list", "dependencies: null\n"},
		{"mapping instead of list", "dependencies: {}\n"},
		{"scalar entry", "dependencies: [lightpanda]\n"},
		{"numeric version", "dependencies: [{name: lightpanda, description: Browser, version: 1.0}]\n"},
		{"boolean description", "dependencies: [{name: lightpanda, description: true}]\n"},
		{"unknown field", "dependencies: [{name: lightpanda, description: Browser, install: shell}]\n"},
		{"duplicate key", "dependencies: [{name: lightpanda, name: helper, description: Browser}]\n"},
		{"duplicate names", "dependencies: [{name: lightpanda, description: Browser}, {name: lightpanda, description: Other}]\n"},
		{"missing description", "dependencies: [{name: lightpanda}]\n"},
		{"untrimmed version", "dependencies: [{name: lightpanda, description: Browser, version: \" 1.0\"}]\n"},
		{"anchored list", "dependencies: &software []\n"},
		{"aliased description", "dependencies: [{name: lightpanda, description: &text Browser}, {name: helper, description: *text}]\n"},
		{"empty actions", "actions: []\n"},
		{"empty responsibilities", "responsibilities: []\n"},
	}
}
