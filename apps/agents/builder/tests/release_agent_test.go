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
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestReleaseAgentStartsWorkflowAndProposesBoundedStatusCheck(t *testing.T) {
	f := newReleaseFixture(t)
	result, output, err := f.execute(t)
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "pending" || output.WorkflowRunID != releaseRunID || output.WorkflowURL != "https://github.com/"+publicationRepository+"/actions/runs/93" || output.Package != "" || output.ReleaseURL != "" {
		t.Fatalf("pending workflow must not advertise an installable package: %+v", output)
	}
	if f.http.dispatches != 1 || len(result.NextActions) != 1 || result.NextActions[0].ActionName != builderactions.ReleaseAgentName || len(result.Artifacts) != 0 {
		t.Fatal("pending release must propose one status check without dispatching it")
	}
	var next builderactions.ReleaseAgentInput
	if err := json.Unmarshal(result.NextActions[0].Input, &next); err != nil {
		t.Fatal(err)
	}
	want := f.input
	want.WorkflowRunID = releaseRunID
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("status proposal changed release identity: got %+v, want %+v", next, want)
	}
	checkpoints, _ := json.Marshal(f.checkpoints.record.Checkpoints)
	if bytes.Contains(checkpoints, []byte(publicationToken)) || bytes.Contains(result.Output, []byte(publicationToken)) || bytes.Contains(result.Output, []byte("untrusted.invalid")) {
		t.Fatal("release output or checkpoints leaked credentials or trusted response URLs")
	}
}

func TestReleaseAgentReturnsPinnedPackageOnlyAfterVerifiedPublication(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			f := newReleaseFixture(t)
			f.input.WorkflowRunID = releaseRunID
			f.http.status, f.http.conclusion, f.http.private = "completed", "success", private
			f.http.redirectManifest = private
			result, output, err := f.execute(t)
			if err != nil {
				t.Fatal(err)
			}
			visibility := "public"
			if private {
				visibility = "private"
			}
			if output.Status != "released" || output.Package != "github.com/"+publicationRepository+"@"+releaseVersion || output.ReleaseURL != "https://github.com/"+publicationRepository+"/releases/tag/"+releaseVersion || output.Repository != publicationRepository || output.CommitSHA != releaseCommit || output.AgentType != releaseAgentType || output.Version != releaseVersion || output.Visibility != visibility {
				t.Fatalf("release result does not identify the verified published package: %+v", output)
			}
			if f.http.dispatches != 0 || len(result.NextActions) != 0 || len(result.Artifacts) != 0 {
				t.Fatal("finished status check dispatched work, proposed installation or fabricated artifacts")
			}
			if private && f.http.cdnRequests != 1 {
				t.Fatal("private release verification did not follow its manifest CDN redirect")
			}
		})
	}
}

func TestReleaseAgentReconcilesDispatchWithoutRepeatingIt(t *testing.T) {
	for _, scenario := range []string{"dispatch acknowledgement lost", "dispatch accepted but not yet listed", "run checkpoint acknowledgement lost"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReleaseFixture(t)
			if scenario != "run checkpoint acknowledgement lost" {
				f.http.lostDispatchAck = true
				f.http.hideRun = scenario == "dispatch accepted but not yet listed"
			} else {
				f.checkpoints.afterSave = func(name string) error {
					if name == "release-run" {
						return errors.New("saved workflow run acknowledgement lost")
					}
					return nil
				}
			}
			first, firstOutput, firstErr := f.execute(t)
			if firstErr != nil && strings.Contains(firstErr.Error(), publicationToken) || bytes.Contains(first.Output, []byte(publicationToken)) {
				t.Fatal("uncertain dispatch exposed a managed credential")
			}
			if f.http.hideRun {
				if firstErr != nil || firstOutput.Status != "uncertain" || len(first.NextActions) != 0 || firstOutput.Package != "" {
					t.Fatalf("accepted but unconfirmed dispatch must stop for inspection: %+v %v", firstOutput, firstErr)
				}
				f.http.hideRun = false
			}
			f.checkpoints.afterSave = nil
			_, output, err := f.execute(t)
			if err != nil || output.Status != "pending" || output.WorkflowRunID != releaseRunID {
				t.Fatalf("saved dispatch did not resolve to the same workflow: %+v %v", output, err)
			}
			if f.http.dispatches != 1 {
				t.Fatalf("retry dispatched %d workflows; want one", f.http.dispatches)
			}
		})
	}
	t.Run("dispatch intent acknowledgement lost", func(t *testing.T) {
		f := newReleaseFixture(t)
		f.checkpoints.afterSave = func(name string) error {
			if name == "release-dispatch-attempt" {
				return errors.New("dispatch intent acknowledgement lost")
			}
			return nil
		}
		if _, _, err := f.execute(t); err == nil {
			t.Fatal("unacknowledged dispatch intent allowed a workflow dispatch")
		}
		f.checkpoints.afterSave = nil
		_, output, err := f.execute(t)
		if err != nil || output.Status != "uncertain" || output.Package != "" || f.http.dispatches != 0 {
			t.Fatalf("uncertain dispatch must require inspection rather than repeating POST: %+v %v", output, err)
		}
	})
}

func TestReleaseAgentWithholdsPackageForFailedOrUnverifiedRelease(t *testing.T) {
	for _, scenario := range []string{"workflow failed", "different workflow commit", "different requested version", "version tag targets different commit", "draft release", "native asset checksum differs", "manifest bytes differ"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReleaseFixture(t)
			f.input.WorkflowRunID = releaseRunID
			f.http.status, f.http.conclusion = "completed", "success"
			switch scenario {
			case "workflow failed":
				f.http.conclusion = "failure"
			case "different workflow commit":
				f.http.headSHA = strings.Repeat("b", 40)
			case "different requested version":
				f.http.titleVersion = "v0.4.0"
			case "version tag targets different commit":
				f.http.tagSHA = strings.Repeat("b", 40)
			case "draft release":
				f.http.draft = true
			case "native asset checksum differs":
				f.http.assets[0]["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "manifest bytes differ":
				f.http.manifest = append(f.http.manifest, '\n')
			}
			result, output, err := f.execute(t)
			if output.Package != "" || output.ReleaseURL != "" || output.Status == "released" || len(result.NextActions) != 0 || f.http.dispatches != 0 {
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
			f := newReleaseFixture(t)
			t.Setenv("GITHUB_TOKEN", "ambient-token-must-not-enable-release")
			switch scenario {
			case "missing managed token":
				f.ctx = action.WithSecrets(f.ctx, publicationSecrets(""))
			case "missing execution":
				f.ctx = action.WithExecution(f.ctx, action.Execution{})
			case "invalid repository":
				f.input.Repository = "https://github.com/fixture/built-agent"
			case "moving default branch":
				f.http.branchSHA = strings.Repeat("b", 40)
			case "workflow guard missing":
				f.http.workflow = strings.ReplaceAll(f.http.workflow, `test "$GITHUB_SHA" = "$EXPECTED_COMMIT"`, "echo skipped")
			case "cancelled":
				ctx, cancel := context.WithCancel(f.ctx)
				cancel()
				f.ctx = ctx
			}
			result, _, err := f.execute(t)
			if err == nil || f.http.dispatches != 0 || len(result.Output) != 0 || strings.Contains(err.Error(), publicationToken) {
				t.Fatalf("unsafe release reached dispatch or leaked its output: %s %v", result.Output, err)
			}
			if scenario == "missing managed token" && f.http.requests != 0 {
				t.Fatal("ambient GITHUB_TOKEN reached the release API")
			}
		})
	}
}

func TestReleaseAgentCancelsStatusReadWithoutAnotherDispatch(t *testing.T) {
	f := newReleaseFixture(t)
	f.input.WorkflowRunID = releaseRunID
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.ctx, f.http.onRunRead = ctx, cancel
	result, output, err := f.execute(t)
	if !errors.Is(err, context.Canceled) || f.http.dispatches != 0 || len(result.NextActions) != 0 || output.Package != "" {
		t.Fatalf("status cancellation must stop without another workflow or package: %+v %v", output, err)
	}
}
