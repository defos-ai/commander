package fixtures

import (
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
)

func InterruptedExecution(t *testing.T, config runner.Config) (*runner.Runner, *fakes.LateExecution, fakes.Invocation, operations.Operation) {
	t.Helper()
	config.TaskTimeout = 30 * time.Millisecond
	execution := fakes.NewLateExecution()
	r := NewRunner(t, config, execution.Executor())
	op := Start(t, r)
	Assign(t, r, op.ID, "inspect")
	original := Receive(t, execution.Started())
	interrupted := AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
	return r, execution, original, interrupted
}

func ObserveLate(t *testing.T, e *fakes.LateExecution) fakes.LateObservation {
	t.Helper()
	select {
	case observation := <-e.Observations():
		return observation
	case <-time.After(5 * time.Second):
		t.Fatal("expected observation of an interrupted execution")
		return fakes.LateObservation{}
	}
}

func LateArtifactOutput(original fakes.Invocation) actions.ActionOutput {
	result := Output(original.Call.ActionName)
	result.Artifacts = []artifacts.Artifact{{ID: id.New(), Name: "Agent source", AgentID: original.Agent,
		ActionName: original.Call.ActionName, CommandID: original.Call.Work.TaskID, CreatedAt: time.Now().UTC(),
		Files: []artifacts.ArtifactFile{{ID: id.New(), Name: "source.tar.gz", MediaType: "application/gzip", Size: 7, SHA256: strings.Repeat("a", 64)}}}}
	return result
}
