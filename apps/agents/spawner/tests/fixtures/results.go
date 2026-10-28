package fixtures

import (
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func SpawnResult() runtimes.Result {
	return runtimes.Result{AgentID: "example-agent-12345678", AgentType: "example-agent", Package: ActionPackage,
		Runtime: runtimes.RuntimeNative, RuntimeDetails: runtimes.NativeRuntimeDetails{PID: 42}, Actions: []actionmodel.Action{}}
}

const ActionPackage = "github.com/defos-ai/commander/apps/agents/example@v0.1.0"
