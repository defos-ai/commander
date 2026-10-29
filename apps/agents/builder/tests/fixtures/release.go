package fixtures

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type Release struct {
	Action      *builderactions.ReleaseAgent
	Input       builderactions.ReleaseAgentInput
	Ctx         context.Context
	Checkpoints *fakes.Checkpoints
	HTTP        *fakes.ReleaseHTTP
}

func NewRelease(t *testing.T) *Release {
	t.Helper()
	executor, err := builderactions.NewReleaseAgent()
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{})
	if err != nil || status != action.Ready {
		t.Fatalf("release discovery should need no external executable: %s %v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "GITHUB_TOKEN" {
		t.Fatal("release must declare managed GITHUB_TOKEN")
	}
	command, err := messages.NewMessage(messages.RunAction, "builder-agent-12345678", actionmodel.ActionCall{ActionName: builderactions.ReleaseAgentName, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := fakes.NewCheckpoints(command)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	ctx = action.WithExecution(ctx, action.Execution{CommandID: command.ID})
	ctx = action.WithSecrets(action.WithCheckpoints(ctx, journal), fakes.PublicationSecrets(PublicationToken))
	httpFixture := fakes.NewReleaseHTTP(t, command.ID, fakes.ReleaseConfig{
		Token: PublicationToken, Repository: PublicationRepository, Commit: ReleaseCommit,
		Version: ReleaseVersion, AgentType: ReleaseAgentType, RunID: ReleaseRunID, Workflow: ReleaseWorkflow,
	})
	assets, data := ReleaseAssets(t)
	httpFixture.Change(func(state *fakes.ReleaseState) { state.Assets, state.Manifest = assets, data })
	previous := http.DefaultTransport
	http.DefaultTransport = httpFixture
	t.Cleanup(func() { http.DefaultTransport = previous })
	return &Release{Action: executor, Input: builderactions.ReleaseAgentInput{Repository: PublicationRepository, CommitSHA: ReleaseCommit, Version: ReleaseVersion, AgentType: ReleaseAgentType}, Ctx: ctx, Checkpoints: journal, HTTP: httpFixture}
}

func (f *Release) Execute(t *testing.T) (action.Result, builderactions.ReleaseAgentOutput, error) {
	t.Helper()
	data, err := json.Marshal(f.Input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Action.Run(f.Ctx, data)
	var output builderactions.ReleaseAgentOutput
	if len(result.Output) != 0 {
		if decodeErr := json.Unmarshal(result.Output, &output); decodeErr != nil {
			t.Fatal(decodeErr)
		}
	}
	return result, output, err
}
