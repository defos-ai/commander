package builder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestReleaseAgentStartsWorkflowAndProposesBoundedStatusCheck(t *testing.T) {
	f := fixtures.NewRelease(t)

	result, output, err := f.Execute(t)

	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "pending" || output.WorkflowRunID != fixtures.ReleaseRunID || output.WorkflowURL != "https://github.com/"+fixtures.PublicationRepository+"/actions/runs/93" || output.Package != "" || output.ReleaseURL != "" {
		t.Fatalf("pending workflow must not advertise an installable package: %+v", output)
	}
	if f.HTTP.DispatchCount() != 1 || len(result.NextActions) != 1 || result.NextActions[0].ActionName != builderactions.ReleaseAgentName || len(result.Artifacts) != 0 {
		t.Fatal("pending release must propose one status check without dispatching it")
	}
	var next builderactions.ReleaseAgentInput
	if err := json.Unmarshal(result.NextActions[0].Input, &next); err != nil {
		t.Fatal(err)
	}
	want := f.Input
	want.WorkflowRunID = fixtures.ReleaseRunID
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("status proposal changed release identity: got %+v, want %+v", next, want)
	}
	checkpoints, _ := json.Marshal(f.Checkpoints.Snapshot())
	if bytes.Contains(checkpoints, []byte(fixtures.PublicationToken)) || bytes.Contains(result.Output, []byte(fixtures.PublicationToken)) || bytes.Contains(result.Output, []byte("untrusted.invalid")) {
		t.Fatal("release output or checkpoints leaked credentials or trusted response URLs")
	}
}

func TestReleaseAgentReturnsPinnedPackageOnlyAfterVerifiedPublication(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			f := fixtures.NewRelease(t)
			f.Input.WorkflowRunID = fixtures.ReleaseRunID
			f.HTTP.Change(func(state *fakes.ReleaseState) {
				state.Status = "completed"
				state.Conclusion = "success"
				state.Private = private
				state.RedirectManifest = private
			})

			result, output, err := f.Execute(t)

			if err != nil {
				t.Fatal(err)
			}
			visibility := "public"
			if private {
				visibility = "private"
			}
			if output.Status != "released" || output.Package != "github.com/"+fixtures.PublicationRepository+"@"+fixtures.ReleaseVersion || output.ReleaseURL != "https://github.com/"+fixtures.PublicationRepository+"/releases/tag/"+fixtures.ReleaseVersion || output.Repository != fixtures.PublicationRepository || output.CommitSHA != fixtures.ReleaseCommit || output.AgentType != fixtures.ReleaseAgentType || output.Version != fixtures.ReleaseVersion || output.Visibility != visibility {
				t.Fatalf("release result does not identify the verified published package: %+v", output)
			}
			if f.HTTP.DispatchCount() != 0 || len(result.NextActions) != 0 || len(result.Artifacts) != 0 {
				t.Fatal("finished status check dispatched work, proposed installation or fabricated artifacts")
			}
			if private && f.HTTP.CDNRequestCount() != 1 {
				t.Fatal("private release verification did not follow its manifest CDN redirect")
			}
		})
	}
}

func TestReleaseAgentReconcilesDispatchWithoutRepeatingIt(t *testing.T) {
	for _, scenario := range []string{"dispatch acknowledgement lost", "dispatch accepted but not yet listed", "run checkpoint acknowledgement lost"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixtures.NewRelease(t)
			if scenario != "run checkpoint acknowledgement lost" {
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.LostDispatchAck = true
					state.HideRun = scenario == "dispatch accepted but not yet listed"
				})
			} else {
				f.Checkpoints.SetAfterSave(func(name string) error {
					if name == "release-run" {
						return errors.New("saved workflow run acknowledgement lost")
					}
					return nil
				})
			}

			first, firstOutput, firstErr := f.Execute(t)

			if firstErr != nil && strings.Contains(firstErr.Error(), fixtures.PublicationToken) || bytes.Contains(first.Output, []byte(fixtures.PublicationToken)) {
				t.Fatal("uncertain dispatch exposed a managed credential")
			}
			if f.HTTP.Snapshot().HideRun {
				if firstErr != nil || firstOutput.Status != "uncertain" || len(first.NextActions) != 0 || firstOutput.Package != "" {
					t.Fatalf("accepted but unconfirmed dispatch must stop for inspection: %+v %v", firstOutput, firstErr)
				}
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.HideRun = false
				})
			}
			f.Checkpoints.SetAfterSave(nil)

			_, output, err := f.Execute(t)

			if err != nil || output.Status != "pending" || output.WorkflowRunID != fixtures.ReleaseRunID {
				t.Fatalf("saved dispatch did not resolve to the same workflow: %+v %v", output, err)
			}
			if f.HTTP.DispatchCount() != 1 {
				t.Fatalf("retry dispatched %d workflows; want one", f.HTTP.DispatchCount())
			}
		})
	}
	t.Run("dispatch intent acknowledgement lost", func(t *testing.T) {
		f := fixtures.NewRelease(t)
		f.Checkpoints.SetAfterSave(func(name string) error {
			if name == "release-dispatch-attempt" {
				return errors.New("dispatch intent acknowledgement lost")
			}
			return nil
		})
		if _, _, err := f.Execute(t); err == nil {
			t.Fatal("unacknowledged dispatch intent allowed a workflow dispatch")
		}
		f.Checkpoints.SetAfterSave(nil)

		_, output, err := f.Execute(t)

		if err != nil || output.Status != "uncertain" || output.Package != "" || f.HTTP.DispatchCount() != 0 {
			t.Fatalf("uncertain dispatch must require inspection rather than repeating POST: %+v %v", output, err)
		}
	})
}

func TestReleaseAgentWithholdsPackageForFailedOrUnverifiedRelease(t *testing.T) {
	for _, scenario := range []string{"workflow failed", "different workflow commit", "different requested version", "version tag targets different commit", "draft release", "native asset checksum differs", "manifest bytes differ"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixtures.NewRelease(t)
			f.Input.WorkflowRunID = fixtures.ReleaseRunID
			f.HTTP.Change(func(state *fakes.ReleaseState) {
				state.Status = "completed"
				state.Conclusion = "success"
			})
			switch scenario {
			case "workflow failed":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.Conclusion = "failure"
				})
			case "different workflow commit":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.HeadSHA = strings.Repeat("b", 40)
				})
			case "different requested version":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.TitleVersion = "v0.4.0"
				})
			case "version tag targets different commit":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.TagSHA = strings.Repeat("b", 40)
				})
			case "draft release":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.Draft = true
				})
			case "native asset checksum differs":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.Assets[0]["digest"] = "sha256:" + strings.Repeat("0", 64)
				})
			case "manifest bytes differ":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.Manifest = append(state.Manifest, '\n')
				})
			}

			result, output, err := f.Execute(t)

			if output.Package != "" || output.ReleaseURL != "" || output.Status == "released" || len(result.NextActions) != 0 || f.HTTP.DispatchCount() != 0 {
				t.Fatalf("unverified release advertised a package or continued automatically: %+v %v", output, err)
			}
			if scenario == "workflow failed" {
				if err != nil || output.Status != "failed" || output.Conclusion != "failure" {
					t.Fatalf("failed workflow should return its status for review: %+v %v", output, err)
				}
			} else if err == nil && output.Status != "failed" {
				t.Fatalf("unverified publication succeeded: %+v", output)
			}
		})
	}
}

func TestReleaseAgentGuardsDispatchAndUsesOnlyManagedToken(t *testing.T) {
	for _, scenario := range []string{"missing managed token", "missing execution", "invalid repository", "moving default branch", "workflow guard missing", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixtures.NewRelease(t)
			t.Setenv("GITHUB_TOKEN", "ambient-token-must-not-enable-release")
			switch scenario {
			case "missing managed token":
				f.Ctx = action.WithSecrets(f.Ctx, fakes.PublicationSecrets(""))
			case "missing execution":
				f.Ctx = action.WithExecution(f.Ctx, action.Execution{})
			case "invalid repository":
				f.Input.Repository = "https://github.com/fixture/built-agent"
			case "moving default branch":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.BranchSHA = strings.Repeat("b", 40)
				})
			case "workflow guard missing":
				f.HTTP.Change(func(state *fakes.ReleaseState) {
					state.Workflow = strings.ReplaceAll(state.Workflow, `test "$GITHUB_SHA" = "$EXPECTED_COMMIT"`, "echo skipped")
				})
			case "cancelled":
				ctx, cancel := context.WithCancel(f.Ctx)
				cancel()
				f.Ctx = ctx
			}

			result, _, err := f.Execute(t)

			if err == nil || f.HTTP.DispatchCount() != 0 || len(result.Output) != 0 || strings.Contains(err.Error(), fixtures.PublicationToken) {
				t.Fatalf("unsafe release reached dispatch or leaked its output: %s %v", result.Output, err)
			}
			if scenario == "missing managed token" && f.HTTP.RequestCount() != 0 {
				t.Fatal("ambient GITHUB_TOKEN reached the release API")
			}
		})
	}
}

func TestReleaseAgentCancelsStatusReadWithoutAnotherDispatch(t *testing.T) {
	f := fixtures.NewRelease(t)
	f.Input.WorkflowRunID = fixtures.ReleaseRunID
	ctx, cancel := context.WithCancel(f.Ctx)
	defer cancel()
	f.Ctx = ctx
	f.HTTP.Change(func(state *fakes.ReleaseState) {
		state.OnRunRead = cancel
	})

	result, output, err := f.Execute(t)

	if !errors.Is(err, context.Canceled) || f.HTTP.DispatchCount() != 0 || len(result.NextActions) != 0 || output.Package != "" {
		t.Fatalf("status cancellation must stop without another workflow or package: %+v %v", output, err)
	}
}
