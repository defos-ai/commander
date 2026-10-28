package mcp_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	hostfixtures "github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures/host"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPLateArtifactRemainsReadableWithoutAnotherExecution(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "active operation"
		if completed {
			name = "completed operation"
		}
		t.Run(name, func(t *testing.T) {
			fixture := hostfixtures.NewLateArtifact(t)
			var op operations.Operation
			fixture.Call(t, "start_operation", map[string]any{"mission_name": "late-artifact", "input": map[string]any{}}, &op)
			var task operations.Task
			fixture.Call(t, "assign_task", map[string]any{"operation_id": op.ID, "action_name": "late-artifact", "input": map[string]any{}}, &task)
			select {
			case <-fixture.Work.Started():
			case <-time.After(5 * time.Second):
				t.Fatal("assigned action did not start before its deadline")
			}
			arguments := map[string]any{"operation_id": op.ID, "task_id": task.ID}
			var interrupted operations.Task
			hostfixtures.AwaitMCPLateArtifact(t, "interrupted task", func() bool {
				fixture.Call(t, "get_task", arguments, &interrupted)
				return interrupted.Status == operations.TaskInterrupted
			})
			if interrupted.Result != nil || interrupted.AgentID != fixture.AgentID {
				t.Fatalf("unexpected interrupted execution: %+v", interrupted)
			}
			var savedReport reports.Report
			if completed {
				fixture.Call(t, "complete_operation", map[string]any{"operation_id": op.ID,
					"output": map[string]any{"limitations": []string{"Execution outcome is unknown"}}}, &op)
				fixture.Call(t, "get_report", map[string]any{"operation_id": op.ID}, &savedReport)
			}
			fixture.Work.Unblock()
			var finished operations.Task
			hostfixtures.AwaitMCPLateArtifact(t, "late task result", func() bool {
				fixture.Call(t, "get_task", arguments, &finished)
				return finished.Status == operations.TaskSucceeded
			})
			if finished.ID != task.ID || finished.CommandID != task.CommandID || finished.AgentID != fixture.AgentID || finished.Result == nil || len(finished.Result.Artifacts) != 1 {
				t.Fatalf("late result changed execution identity or lost evidence: %+v", finished)
			}
			ref := finished.Result.Artifacts[0]
			assertLateArtifactResource(t, fixture, ref)
			saved, err := fixture.Executions.Get(fixture.Context, fixture.AgentID, task.CommandID)
			if err != nil || saved.Result == nil {
				t.Fatalf("accepted execution result is missing: %v", err)
			}
			var accepted actionmodel.ActionOutput
			if err := json.Unmarshal(saved.Result.Payload, &accepted); err != nil || len(accepted.Artifacts) != 1 || !accepted.Artifacts[0].Equal(ref) {
				t.Fatalf("MCP changed the accepted artifact or file identity: %+v, %v", accepted, err)
			}
			var refreshed struct {
				operations.Operation
				Progress struct {
					FinishedTasks int  `json:"finished_tasks"`
					Settled       bool `json:"settled"`
				} `json:"progress"`
			}
			fixture.Call(t, "get_operation", map[string]any{"operation_id": op.ID}, &refreshed)
			if len(refreshed.Tasks) != 1 || !reflect.DeepEqual(refreshed.Tasks[0].Result, finished.Result) || refreshed.Progress.FinishedTasks != 1 {
				t.Fatalf("operation detail lost its late task artifact: %+v", refreshed)
			}
			if completed {
				var report reports.Report
				fixture.Call(t, "get_report", map[string]any{"operation_id": op.ID}, &report)
				if refreshed.Status != operations.Completed || !refreshed.Progress.Settled || refreshed.ReportID != op.ReportID || !reflect.DeepEqual(report, savedReport) {
					t.Fatalf("late evidence changed finalization or report: %+v", refreshed)
				}
			} else {
				if refreshed.Status != operations.Planning || refreshed.Progress.Settled {
					t.Fatalf("successful task unexpectedly completed its operation: %+v", refreshed)
				}
				fixture.Call(t, "cancel_operation", map[string]any{"operation_id": op.ID}, &op)
			}
			if fixture.Work.Invocations() != 1 {
				t.Fatalf("late recovery executed action %d times", fixture.Work.Invocations())
			}
		})
	}
}

func assertLateArtifactResource(t *testing.T, f *hostfixtures.LateArtifact, ref artifacts.Artifact) {
	t.Helper()
	uri := "missions://artifacts/" + ref.ID
	resource, err := f.Session.ReadResource(f.Context, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("late artifact metadata unreadable: %v", err)
	}
	var metadata artifacts.Artifact
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &metadata); err != nil || !metadata.Equal(ref) || len(metadata.Files) != 1 {
		t.Fatalf("late artifact metadata differs from task result: %+v, %v", metadata, err)
	}
	file := metadata.Files[0]
	resource, err = f.Session.ReadResource(f.Context, &sdkmcp.ReadResourceParams{URI: uri + "/files/" + file.ID})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("late artifact bytes unreadable: %v", err)
	}
	data := []byte(resource.Contents[0].Text)
	digest := sha256.Sum256(data)
	if !bytes.Equal(data, []byte(hostfixtures.LateArtifactContents)) || int64(len(data)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
		t.Fatal("late artifact download lost its original bytes or checksum")
	}
}
