package missiontest_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	missiontest "github.com/defos-ai/commander/framework/testing/missiontest"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/missiontest"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/missiontest"
)

func TestMissionDriverUsesExplicitDecisionsAndKeepsUnrelatedWork(t *testing.T) {
	stack := fixtures.NewStack(t, "flow")
	var unfinishedID string

	t.Run("scripted mission", func(t *testing.T) {
		driver := missiontest.Connect(t, stack.Config)
		definitions := driver.LoadMissions(fixtures.MissionFile(t))
		driver.WaitForActions("inspect", "report")

		op := driver.Start(definitions[0].Name, json.RawMessage(`{"source":"fixture"}`))
		assigned := driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: json.RawMessage(`{"source":"fixture"}`),
			Selector: &operations.TaskSelector{Hostname: "fixture-host"}})
		waiting := driver.WaitFor(op.ID, func(current operations.Operation) bool {
			return len(current.Questions) == 1 && current.Questions[0].Pending()
		})

		if waiting.Tasks[0].Status != operations.TaskSucceeded || waiting.Tasks[0].Continuation.Status != operations.ContinuationWaiting {
			t.Fatalf("driver did not retain the operator's pending decision: %+v", waiting.Tasks)
		}
		if snapshot := stack.API.Snapshot(); snapshot.Answer["answer"] != nil || snapshot.Resolved != nil {
			t.Fatal("observation answered or resolved work without a test decision")
		}

		driver.Answer(op.ID, waiting.Questions[0].ID, "Publish")
		review := driver.WaitForContinuation(op.ID, assigned.ID)
		resolved := driver.Resolve(op.ID, review, actions.NextAction{ActionName: "report", Input: json.RawMessage(`{"accepted":true}`)})
		if len(resolved.Tasks) != 2 {
			t.Fatalf("explicit follow-up was not retained: %+v", resolved.Tasks)
		}
		reportTask := driver.WaitForTask(op.ID, resolved.Tasks[1].ID)
		if reportTask.Result == nil || len(reportTask.Result.Artifacts) != 1 || len(reportTask.Result.Artifacts[0].Files) != 1 {
			t.Fatalf("report task lost its accepted output or evidence: %+v", reportTask)
		}
		driver.Resolve(op.ID, reportTask)
		artifact := reportTask.Result.Artifacts[0]
		data := driver.Download(artifact.ID, artifact.Files[0])
		completed := driver.Complete(op.ID, reportTask.Result.Output)
		report := driver.Report(op.ID)
		unfinishedID = driver.Start(definitions[0].Name, json.RawMessage(`{}`)).ID

		if completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator || completed.ReportID != report.ID {
			t.Fatalf("explicit operator completion was not retained: operation=%+v report=%+v", completed, report)
		}
		if !bytes.Equal(data, fakes.Evidence()) || !bytes.Equal(report.Output, data) {
			t.Fatalf("accepted report and verified artifact disagree: output=%s downloaded=%s", report.Output, data)
		}
		if snapshot := stack.API.Snapshot(); snapshot.Assigned.Selector == nil || snapshot.Assigned.Selector.Hostname != "fixture-host" || len(snapshot.Resolved) != 1 || snapshot.Resolved[0].ActionName != "report" {
			t.Fatalf("driver changed the supplied executor filter or continuation: %+v", snapshot)
		}
	})

	snapshot := stack.API.Snapshot()
	if snapshot.Starts != 2 || len(snapshot.Canceled) != 1 || snapshot.Canceled[0] != unfinishedID || snapshot.Unauthorized != 0 {
		t.Fatalf("cleanup changed completed or unrelated work: %+v", snapshot)
	}
	if len(snapshot.Answer) != 1 || len(snapshot.Completion) != 1 {
		t.Fatalf("API submissions must leave answer/completion authority to the server: answer=%v complete=%v", snapshot.Answer, snapshot.Completion)
	}
}

func TestMissionDriverRejectsUncertainWritesAndUnverifiedEvidence(t *testing.T) {
	for _, mode := range []string{"uncertain", "corrupt", "redirect", "waiting"} {
		t.Run(mode, func(t *testing.T) {
			stack := fixtures.NewStack(t, mode)

			output, err := fixtures.FailureProcess(t, mode, stack.Config)

			if err == nil {
				t.Fatalf("driver accepted the %s response: %s", mode, output)
			}
			for _, sensitive := range []string{fakes.Password, fakes.BodySecret} {
				if strings.Contains(string(output), sensitive) {
					t.Fatal("driver diagnostics exposed credential or response bytes")
				}
			}
			snapshot := stack.API.Snapshot()
			switch mode {
			case "uncertain":
				if snapshot.Starts != 1 || len(snapshot.Canceled) != 1 || snapshot.Canceled[0] == "" || snapshot.Canceled[0] == fakes.ForeignOperationID {
					t.Fatalf("uncertain start was retried or its accepted identity was lost: %+v; child: %s", snapshot, output)
				}
			case "waiting":
				if !strings.Contains(string(output), "pending question=") || !strings.Contains(string(output), "review task=") {
					t.Fatalf("timeout lost the pending question or continuation: %s", output)
				}
				if snapshot.Starts != 1 || len(snapshot.Canceled) != 1 || len(snapshot.Answer) != 0 || len(snapshot.Completion) != 0 || len(snapshot.Resolved) != 0 {
					t.Fatalf("waiting changed unresolved decisions or lost owned cleanup: %+v", snapshot)
				}
			default:
				if snapshot.Downloads != 1 {
					t.Fatalf("evidence was not fetched exactly once: %+v; child: %s", snapshot, output)
				}
			}
			if stack.Target != nil && stack.Target.Requests() != 0 {
				t.Fatal("artifact redirect reached another origin")
			}
		})
	}
}

func TestMissiontestFailureProcess(t *testing.T) {
	mode := fixtures.FailureMode()
	if mode == "" {
		return
	}
	settings := fixtures.FailureConfig(t)
	if mode == "waiting" {
		settings.Timeout = 200 * time.Millisecond
	}
	driver := missiontest.Connect(t, settings)
	if mode == "uncertain" {
		driver.Start("missiontest-inspection", json.RawMessage(`{}`))
		return
	}
	if mode == "waiting" {
		op := driver.Start("missiontest-inspection", json.RawMessage(`{}`))
		driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: json.RawMessage(`{}`)})
		driver.WaitFor(op.ID, func(operations.Operation) bool { return false })
		return
	}
	artifact := fakes.Artifact()
	driver.Download(artifact.ID, artifact.Files[0])
}
