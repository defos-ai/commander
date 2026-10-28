package fakes

import (
	"context"
	"errors"
	"slices"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

type SpawnInvocation struct{ RequestID, Reference string }

type ActionLauncher struct {
	calls  []SpawnInvocation
	result runtimes.Result
	err    error
}

func (launcher *ActionLauncher) Spawn(_ context.Context, requestID, reference string) (runtimes.Result, error) {
	launcher.calls = append(launcher.calls, SpawnInvocation{RequestID: requestID, Reference: reference})
	return launcher.Result(), launcher.err
}

type RejectArtifacts struct{}

func (RejectArtifacts) Put(context.Context, string, artifacts.FileSource) (artifacts.ArtifactFile, error) {
	return artifacts.ArtifactFile{}, errors.New("unexpected artifact write")
}

func (RejectArtifacts) Get(context.Context, string, artifacts.ArtifactFile) ([]byte, error) {
	return nil, errors.New("unexpected artifact read")
}

type ActionLauncherConfig struct {
	Result runtimes.Result
	Error  error
}

func NewActionLauncher(config ActionLauncherConfig) *ActionLauncher {
	launcher := &ActionLauncher{result: config.Result, err: config.Error}
	launcher.result = launcher.Result()
	return launcher
}
func (launcher *ActionLauncher) Calls() []SpawnInvocation {
	return append([]SpawnInvocation{}, launcher.calls...)
}
func (launcher *ActionLauncher) Result() runtimes.Result {
	result := launcher.result
	result.Actions = slices.Clone(result.Actions)
	for i := range result.Actions {
		result.Actions[i] = result.Actions[i].Clone()
	}
	return result
}
