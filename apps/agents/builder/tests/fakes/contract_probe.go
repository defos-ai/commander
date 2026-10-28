package fakes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Only the constructor probe uses real Go. The ordinary fixture checks still
// assert production's isolated environment before this test-only delegation.
func runRealContractProbe(args []string, config BuildConfig) error {
	definitions := config.Protocol.Actions
	if config.Mode == "contracts-real-missing" {
		definitions = definitions[:1]
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return err
	}
	source := fmt.Sprintf(`package generated

import (
	"context"
	"encoding/json"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const AgentType = %q

func NewAgent(config agent.AgentConfig) (*agent.Agent, error) {
	var definitions []actionmodel.Definition
	if err := json.Unmarshal([]byte(%q), &definitions); err != nil {
		return nil, err
	}
	config.Type = AgentType
	for _, definition := range definitions {
		config.Actions = append(config.Actions, &contractAction{Base: action.NewBase(actionmodel.Action{
			Name: definition.Name, Description: definition.Description,
			InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema,
			RequiredSecrets: definition.RequiredSecrets,
		})})
	}
	return agent.NewAgent(config)
}

type contractAction struct { *action.Base }

func (*contractAction) Discover(context.Context, environment.Environment, *environment.DiscoveryScope) (action.Status, error) {
	panic("discovery must not run during contract inspection")
}

func (*contractAction) Run(context.Context, json.RawMessage) (action.Result, error) {
	panic("action execution must not run during contract inspection")
}
`, config.Protocol.AgentType, string(encoded))
	if err := os.WriteFile(filepath.Join(config.Source, "agent.go"), []byte(source), 0600); err != nil {
		return err
	}
	module := "module example.invalid/fixture\n\ngo 1.26.7\n\nrequire github.com/defos-ai/commander/framework v0.1.0\n"
	if err := os.WriteFile(filepath.Join(config.Source, "go.mod"), []byte(module), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, config.ProbeGo, args...)
	command.Dir = config.Source
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOWORK", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "CGO_ENABLED", "GOFLAGS":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "GOWORK="+config.ProbeWorkspace,
		"GOMODCACHE="+config.ProbeModuleCache, "GOCACHE="+config.ProbeCache,
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOFLAGS=")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}
