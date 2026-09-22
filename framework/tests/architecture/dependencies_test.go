package architecture_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDependencyBoundaries(t *testing.T) {
	// Arrange: each application shares domain contracts without importing the
	// runtime of the application it communicates with.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	implementations := []string{
		"commander/framework/agent",
		"commander/framework/controller",
		"commander/apps/agents",
		"commander/apps/controllers",
		"commander/adapters",
		"commander/providers",
	}
	for _, scenario := range []struct {
		name      string
		directory string
		packages  []string
		forbidden []string
	}{
		{
			name:      "domain contracts",
			directory: filepath.Join(root, "framework"),
			packages:  []string{"./domain/..."},
			forbidden: implementations,
		},
		{
			name:      "messaging contracts and clients",
			directory: filepath.Join(root, "framework"),
			packages:  []string{"./comms/..."},
			forbidden: implementations,
		},
		{
			name:      "controller runtime",
			directory: filepath.Join(root, "framework"),
			packages:  []string{"./controller/..."},
			forbidden: []string{"commander/framework/agent", "commander/apps/agents", "commander/apps/controllers", "commander/adapters", "commander/providers"},
		},
		{
			name:      "agent runtime",
			directory: filepath.Join(root, "framework"),
			packages:  []string{"./agent/..."},
			forbidden: []string{"commander/framework/controller", "commander/apps/agents", "commander/apps/controllers", "commander/adapters", "commander/providers"},
		},
		{
			name:      "MCP adapter",
			directory: filepath.Join(root, "apps/adapters/mcp"),
			packages:  []string{"./..."},
			forbidden: []string{"commander/framework/agent", "commander/framework/controller", "commander/apps/agents", "commander/apps/controllers"},
		},
		{
			name:      "default controller executable",
			directory: filepath.Join(root, "apps/controllers/default"),
			packages:  []string{"./cmd"},
			forbidden: []string{"commander/framework/agent", "commander/apps/agents", "commander/adapters"},
		},
		{
			name:      "web quality agent executable",
			directory: filepath.Join(root, "apps/agents/web-quality"),
			packages:  []string{"./cmd"},
			forbidden: []string{"commander/framework/controller", "commander/apps/controllers", "commander/adapters", "commander/providers/controller", "commander/providers/fs/controller"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			args := append([]string{"list", "-mod=readonly", "-deps"}, scenario.packages...)
			command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
			command.Dir = scenario.directory
			command.Env = append(os.Environ(), "GOWORK=off")

			// Act: resolve the full production dependency graph, including imports
			// introduced indirectly through factories or shared contracts.
			output, err := command.CombinedOutput()

			// Assert: callers depend on shared contracts, never another runtime.
			if err != nil {
				t.Fatalf("resolve dependencies: %v\n%s", err, output)
			}
			dependencies := strings.Fields(string(output))
			foundPackage := false
			for _, dependency := range dependencies {
				foundPackage = foundPackage || strings.HasPrefix(dependency, "commander/")
				for _, forbidden := range scenario.forbidden {
					if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
						t.Errorf("unexpected dependency on %s", dependency)
					}
				}
			}
			if !foundPackage {
				t.Fatalf("no Commander packages matched the dependency check:\n%s", output)
			}
		})
	}
}
