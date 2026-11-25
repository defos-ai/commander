package missiontest_test

import (
	"bytes"
	"encoding/json"
	"slices"
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
	var unfinishedID, cancelledID string

	t.Run("scripted mission", func(t *testing.T) {
		driver := missiontest.Connect(t, stack.Config)
		definitions := driver.LoadMissions(fixtures.MissionFile(t))
		driver.WaitForActions("inspect", "report")

		op := driver.Start(definitions[0].Name, json.RawMessage(`{"source":"fixture"}`))
		assigned := driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: map[string]any{"source": "fixture"},
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
		if completed.Status != operations.Completed || completed.CompletedBy != operations.CompletionOperator || completed.ReportID != report.ID {
			t.Fatalf("explicit operator completion was not retained: operation=%+v report=%+v", completed, report)
		}
		if !bytes.Equal(data, fakes.Evidence()) || !bytes.Equal(report.Output, data) {
			t.Fatalf("accepted report and verified artifact disagree: output=%s downloaded=%s", report.Output, data)
		}
		if snapshot := stack.API.Snapshot(); snapshot.Assigned.Selector == nil || snapshot.Assigned.Selector.Hostname != "fixture-host" || len(snapshot.Resolved) != 1 || snapshot.Resolved[0].ActionName != "report" {
			t.Fatalf("driver changed the supplied executor filter or continuation: %+v", snapshot)
		}
		if !bytes.Equal(assigned.Input, json.RawMessage(`{"source":"fixture"}`)) {
			t.Fatalf("structured assignment input changed: %s", assigned.Input)
		}
		unfinishedID = driver.Start(definitions[0].Name, json.RawMessage(`{}`)).ID
		driver.Assign(unfinishedID, missiontest.Assignment{ActionName: "inspect", Input: struct {
			Source string `json:"source"`
		}{Source: "cleanup"}})
	})

	t.Run("already cancelled work", func(t *testing.T) {
		driver := missiontest.Connect(t, stack.Config)
		op := driver.Start("missiontest-inspection", map[string]any{})
		driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: map[string]any{"source": "cleanup"}})

		driver.Cancel(op.ID)
		cancelledID = op.ID
	})

	snapshot := stack.API.Snapshot()
	if snapshot.Starts != 3 || len(snapshot.Canceled) != 2 || !slices.Contains(snapshot.Canceled, unfinishedID) || !slices.Contains(snapshot.Canceled, cancelledID) || snapshot.Unauthorized != 0 {
		t.Fatalf("cleanup changed completed or unrelated work: %+v", snapshot)
	}
	for _, operationID := range []string{unfinishedID, cancelledID} {
		operation := snapshot.Operations[operationID]
		if operation.Status != operations.Cancelled || len(operation.Tasks) != 1 || operation.Tasks[0].Status != operations.TaskSucceeded ||
			snapshot.Reads[operationID] < 3 || !slices.Contains(snapshot.Drained, operationID) || slices.Contains(snapshot.Work, operationID) {
			t.Fatalf("cleanup returned before owned execution settled: %+v", operation)
		}
		if !bytes.Equal(operation.Tasks[0].Input, json.RawMessage(`{"source":"cleanup"}`)) {
			t.Fatalf("cleanup assignment input changed: %s", operation.Tasks[0].Input)
		}
	}
	if snapshot.Operations[fakes.ForeignOperationID].Status != operations.Running || !slices.Contains(snapshot.Work, fakes.ForeignOperationID) {
		t.Fatal("cleanup changed another operation's work")
	}
	if len(snapshot.Answer) != 1 || len(snapshot.Completion) != 1 {
		t.Fatalf("API submissions must leave answer/completion authority to the server: answer=%v complete=%v", snapshot.Answer, snapshot.Completion)
	}
	if deadline := stack.Transport.MaxRemaining(); deadline <= 10*time.Second {
		t.Fatalf("driver shortened the supplied fifteen-second HTTP timeout: %s", deadline)
	}
}

func TestMissionDriverRejectsUncertainWritesAndUnverifiedEvidence(t *testing.T) {
	for _, mode := range []string{"uncertain", "corrupt", "redirect", "waiting", "conflict", "unsettled", "late_predicate"} {
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
				if !strings.Contains(string(output), "operation_write_uncertain") || !strings.Contains(string(output), "outcome may be unknown") {
					t.Fatalf("uncertain start lost its error code or readback guidance: %s", output)
				}
				if snapshot.Starts != 1 || len(snapshot.Canceled) != 1 || snapshot.Canceled[0] == "" || snapshot.Canceled[0] == fakes.ForeignOperationID {
					t.Fatalf("uncertain start was retried or its accepted identity was lost: %+v; child: %s", snapshot, output)
				}
			case "waiting", "late_predicate":
				if !strings.Contains(string(output), "pending question=") || !strings.Contains(string(output), "review task=") {
					t.Fatalf("timeout lost the pending question or continuation: %s", output)
				}
				if snapshot.Starts != 1 || len(snapshot.Canceled) != 1 || len(snapshot.Answer) != 0 || len(snapshot.Completion) != 0 || len(snapshot.Resolved) != 0 {
					t.Fatalf("waiting changed unresolved decisions or lost owned cleanup: %+v", snapshot)
				}
				if mode == "late_predicate" && !strings.Contains(string(output), "context deadline exceeded") {
					t.Fatalf("predicate reported success after its deadline: %s", output)
				}
			case "conflict":
				if !strings.Contains(string(output), "continuation_conflict") || strings.Contains(string(output), "outcome may be unknown") ||
					snapshot.Resolutions != 1 || len(snapshot.Resolved) != 0 || len(snapshot.Canceled) != 1 {
					t.Fatalf("confirmed conflict was retried or misreported: %+v; child: %s", snapshot, output)
				}
			case "unsettled":
				if !strings.Contains(string(output), "interrupted") || snapshot.Starts != 1 || len(snapshot.Canceled) != 1 || len(snapshot.Drained) != 0 {
					t.Fatalf("cleanup accepted unknown execution outcomes: %+v; child: %s", snapshot, output)
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
	if mode == "waiting" || mode == "late_predicate" {
		settings.Timeout = 200 * time.Millisecond
	}
	if mode == "unsettled" {
		settings.PollInterval = 50 * time.Millisecond
	}
	driver := missiontest.Connect(t, settings)
	if mode == "uncertain" {
		driver.Start("missiontest-inspection", json.RawMessage(`{}`))
		return
	}
	if mode == "waiting" || mode == "late_predicate" {
		op := driver.Start("missiontest-inspection", json.RawMessage(`{}`))
		driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: json.RawMessage(`{}`)})
		driver.WaitFor(op.ID, func(current operations.Operation) bool {
			if mode == "late_predicate" && len(current.Questions) > 0 {
				time.Sleep(2 * settings.Timeout)
				return true
			}
			return false
		})
		return
	}
	if mode == "conflict" {
		op := driver.Start("missiontest-inspection", map[string]any{})
		driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: map[string]any{}})
		waiting := driver.WaitFor(op.ID, func(current operations.Operation) bool { return len(current.Questions) > 0 })
		driver.Answer(op.ID, waiting.Questions[0].ID, "Publish")
		review := driver.WaitForContinuation(op.ID, waiting.Tasks[0].ID)
		review.Continuation.Version--
		driver.Resolve(op.ID, review)
		return
	}
	if mode == "unsettled" {
		op := driver.Start("missiontest-inspection", map[string]any{})
		driver.Assign(op.ID, missiontest.Assignment{ActionName: "inspect", Input: map[string]any{}})
		driver.Cancel(op.ID)
		return
	}
	artifact := fakes.Artifact()
	driver.Download(artifact.ID, artifact.Files[0])
}
