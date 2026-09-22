package agents_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPrivateAgentUsesFrameworkWithoutConcreteApplications(t *testing.T) {
	// Arrange: an independent module supplies its own action and infrastructure.
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	module := fmt.Sprintf("module private.example/agent\n\ngo 1.26.7\n\nrequire commander/framework v0.0.0\nreplace commander/framework => %q\n", filepath.ToSlash(root))
	for name, contents := range map[string]string{"go.mod": module, "agent.go": privateAgentSource} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
		cmd.Dir = directory
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
		return cmd
	}

	// Act: compile outside this module, then inspect its transitive dependencies.
	output, err := command("build", "-mod=mod", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("build private agent: %v\n%s", err, output)
	}
	dependencies, err := command("list", "-deps", "./...").CombinedOutput()

	// Assert: importing the framework neither requires internal access nor pulls
	// in Commander applications, adapters, or infrastructure implementations.
	if err != nil {
		t.Fatalf("list private dependencies: %v\n%s", err, dependencies)
	}
	for _, dependency := range strings.Fields(string(dependencies)) {
		for _, forbidden := range []string{"commander/apps/agents/", "commander/apps/controllers/", "commander/adapters/", "commander/providers/", "commander/framework/controller"} {
			if strings.HasPrefix(dependency, forbidden) {
				t.Errorf("private agent unexpectedly imports %s", dependency)
			}
		}
	}
}

const privateAgentSource = `package privateagent

import (
    "context"
    "commander/framework/agent"
    "commander/framework/agent/action"
    actionmodel "commander/framework/domain/actions"
    "commander/framework/artifact/store"
    "commander/framework/comms"
)

type Input struct { Value string }
type Output struct { Value string }

func Serve(ctx context.Context, bus comms.Bus, artifacts store.ArtifactStore) error {
    echo, err := action.NewFunc(action.FuncConfig[Input, Output]{
        Action: actionmodel.Action{Name: "private-echo"},
        Run: func(ctx context.Context, input Input) (Output, error) {
            return Output{Value: input.Value}, ctx.Err()
        },
    })
    if err != nil { return err }
    instance, err := agent.NewAgent(agent.AgentConfig{
        Type: "private", ArtifactStore: artifacts, Actions: []action.Executor{echo},
    })
    if err != nil { return err }
    return instance.Serve(ctx, bus, nil)
}
`
