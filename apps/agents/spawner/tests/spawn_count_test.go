package packages_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestSpawnAgentDefaultsAndExplicitSingleCountKeepLaunchResult(t *testing.T) {
	for _, count := range []string{"", `,"count":1`} {
		t.Run("input"+count, func(t *testing.T) {
			launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
			executor := fixtures.SpawnAction(t, launcher)
			execution := fixtures.SourceApprovalExecution()
			ctx := action.WithExecution(t.Context(), execution)

			result, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.ActionPackage+`"`+count+`}`))

			if err != nil {
				t.Fatal(err)
			}
			var output runtimes.Result
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(output, fixtures.SpawnResult()) || len(result.NextActions) != 0 {
				t.Fatalf("single count changed the flat launch result: output=%s proposals=%+v", result.Output, result.NextActions)
			}
			calls := launcher.Calls()
			if len(calls) != 1 || calls[0] != (fakes.SpawnInvocation{RequestID: "command:" + execution.CommandID, Reference: fixtures.ActionPackage}) {
				t.Fatalf("single count did not launch once under its execution identity: %+v", calls)
			}
		})
	}
}

func TestSpawnAgentMaximumBatchProposesIndependentSingleExecutions(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)
	secretRequests := 0
	ctx := action.WithSecrets(action.WithExecution(t.Context(), fixtures.SourceApprovalExecution()), fakes.SecretRequester(func(context.Context, string) ([]byte, error) {
		secretRequests++
		return []byte("fixture-package-token"), nil
	}))

	planned, err := executor.Run(ctx, json.RawMessage(`{"package":"`+fixtures.UntrustedSource+`","visibility":"private","count":100}`))

	if err != nil {
		t.Fatal(err)
	}
	if calls := launcher.Calls(); len(calls) != 0 || secretRequests != 0 {
		t.Fatalf("parent requested secrets or launched before child dispatch: secret_requests=%d launches=%+v", secretRequests, calls)
	}
	var output struct {
		Package    string                   `json:"package"`
		Visibility agentpackages.Visibility `json:"visibility"`
		Count      int                      `json:"count"`
	}
	if err := json.Unmarshal(planned.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Package != fixtures.UntrustedSource || output.Visibility != agentpackages.Private || output.Count != 100 || len(planned.NextActions) != 100 {
		t.Fatalf("batch plan lost its requested package, visibility or size: output=%+v proposals=%d", output, len(planned.NextActions))
	}
	for _, proposal := range planned.NextActions {
		var input map[string]json.RawMessage
		if err := json.Unmarshal(proposal.Input, &input); err != nil {
			t.Fatal(err)
		}
		var reference string
		var visibility agentpackages.Visibility
		if err := json.Unmarshal(input["package"], &reference); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(input["visibility"], &visibility); err != nil {
			t.Fatal(err)
		}
		if proposal.ActionName != spawneractions.SpawnAgentName || len(input) != 2 || reference != fixtures.UntrustedSource || visibility != agentpackages.Private {
			t.Fatalf("batch proposed recursive or changed work: %+v", proposal)
		}
		questions := fakes.NewSourceQuestions()
		answer := fixtures.SourceApprovalAnswer("Yes", operations.AnswerOperator)
		answer.Text = fixtures.PrivateSourceApprovalQuestion
		questions.Answer(answer, nil)
		child := action.WithQuestions(action.WithExecution(ctx, fixtures.SourceApprovalExecution()), questions)

		result, err := executor.Run(child, proposal.Input)

		if err != nil || len(result.NextActions) != 0 {
			t.Fatalf("controller-dispatched child could not perform one launch: err=%v proposals=%+v", err, result.NextActions)
		}
		request := receiveSourceQuestion(t, child, questions)
		if request.Text != fixtures.PrivateSourceApprovalQuestion {
			t.Fatalf("child did not request its own source approval: %+v", request)
		}
	}
	calls := launcher.Calls()
	if len(calls) != 100 || secretRequests != 100 {
		t.Fatalf("children did not separately acquire credentials and launch: launches=%d secret_requests=%d", len(calls), secretRequests)
	}
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		commandID, validPrefix := strings.CutPrefix(call.RequestID, "command:")
		if !validPrefix || !id.Valid(commandID) || seen[call.RequestID] || call.Reference != fixtures.UntrustedSource {
			t.Fatalf("batch children did not retain independent execution identities: %+v", calls)
		}
		seen[call.RequestID] = true
	}
}

func TestSpawnAgentRejectsInvalidCountsBeforeLaunchOrProposals(t *testing.T) {
	launcher := fakes.NewActionLauncher(fakes.ActionLauncherConfig{Result: fixtures.SpawnResult()})
	executor := fixtures.SpawnAction(t, launcher)
	ctx := action.WithExecution(t.Context(), fixtures.SourceApprovalExecution())
	for _, count := range []string{"0", "-1", "101", "null", "1.5"} {
		t.Run(count, func(t *testing.T) {
			input := json.RawMessage(fmt.Sprintf(`{"package":%q,"count":%s}`, fixtures.ActionPackage, count))

			result, err := executor.Run(ctx, input)

			if err == nil || len(result.NextActions) != 0 || len(launcher.Calls()) != 0 {
				t.Fatalf("invalid batch size produced executable work: count=%s err=%v proposals=%+v calls=%+v", count, err, result.NextActions, launcher.Calls())
			}
		})
	}
}
