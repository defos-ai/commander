package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestAPILateExecutionArtifactRemainsDownloadable(t *testing.T) {
	for _, terminal := range []string{"", "completed", "cancelled"} {
		name := terminal
		if name == "" {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			server, worker, work, executionStore := hostfixtures.StartLateArtifactAPI(t, terminal != "")
			operation := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost,
				"/api/v1/operations", `{"mission_name":"late-artifact","input":{}}`), http.StatusCreated)
			path := "/api/v1/operations/" + operation.ID
			queued := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodPost,
				path+"/tasks", `{"action_name":"late-artifact","input":{}}`), http.StatusCreated)
			select {
			case <-work.Started():
			case <-time.After(5 * time.Second):
				t.Fatal("artifact action did not start before its deadline")
			}
			var interrupted models.TaskResponse
			hostfixtures.AwaitSecretCondition(t, "public task interruption", func() bool {
				interrupted = decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodGet,
					path+"/tasks/"+queued.ID, ""))
				return interrupted.Status == "interrupted"
			})
			if interrupted.Result != nil || interrupted.Error == "" || interrupted.AgentID != worker.ID() {
				t.Fatalf("interrupted execution acquired a result or lost its owner: %+v", interrupted)
			}
			var finalized models.OperationResponse
			var report models.ReportResponse
			if terminal != "" {
				endpoint, body := "/cancel", ""
				if terminal == "completed" {
					endpoint, body = "/complete", `{"output":{"limitations":["Waiting for the original execution outcome"]}}`
				}
				finalized = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, path+endpoint, body))
				if finalized.Status != terminal || finalized.Progress.Settled {
					t.Fatalf("finalization hid the unknown execution: %+v", finalized)
				}
				if terminal == "completed" {
					report = decodeResponse[models.ReportResponse](t, secretRequest(t, server, http.MethodGet, path+"/report", ""))
				}
			}
			work.Unblock()
			var recovered models.OperationResponse
			hostfixtures.AwaitSecretCondition(t, "late result and artifact through public API", func() bool {
				recovered = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, path, ""))
				return len(recovered.Tasks) == 1 && recovered.Tasks[0].Status == "succeeded"
			})
			task := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodGet, path+"/tasks/"+queued.ID, ""))
			if task.ID != queued.ID || task.CommandID != queued.CommandID || task.AgentID != worker.ID() ||
				!reflect.DeepEqual(task.DeadlineAt, interrupted.DeadlineAt) || task.Error != "" || task.Result == nil ||
				len(task.Result.Artifacts) != 1 || !reflect.DeepEqual(task, recovered.Tasks[0]) {
				t.Fatalf("late result did not restore the original task: %+v", task)
			}
			ref := task.Result.Artifacts[0]
			if len(ref.Files) != 1 || ref.AgentID != worker.ID() || ref.CommandID != queued.CommandID || ref.ActionName != "late-artifact" {
				t.Fatalf("late result lost artifact ownership or files: %+v", ref)
			}
			metadata := decodeResponse[models.ArtifactResponse](t, secretRequest(t, server, http.MethodGet, "/api/v1/artifacts/"+ref.ID, ""))
			if !reflect.DeepEqual(metadata, ref) {
				t.Fatalf("artifact detail differs from task result: %+v", metadata)
			}
			download := server.Request(t, http.MethodGet, "/api/v1/artifacts/"+ref.ID+"/files/"+ref.Files[0].ID, hostfixtures.Authenticate)
			digest := sha256.Sum256([]byte(hostfixtures.LateArtifactContents))
			if download.Status != http.StatusOK || download.Body != hostfixtures.LateArtifactContents ||
				ref.Files[0].Size != int64(len(hostfixtures.LateArtifactContents)) || ref.Files[0].SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("late artifact bytes do not match their saved descriptor: status=%d file=%+v", download.Status, ref.Files[0])
			}
			saved, err := executionStore.Get(t.Context(), worker.ID(), queued.CommandID)
			if err != nil || saved.Result == nil {
				t.Fatalf("late execution outcome was not durable: %+v, %v", saved, err)
			}
			var accepted actionmodel.ActionOutput
			if err := json.Unmarshal(saved.Result.Payload, &accepted); err != nil || len(accepted.Artifacts) != 1 ||
				len(accepted.Artifacts[0].Files) != 1 ||
				accepted.Artifacts[0].ID != ref.ID || accepted.Artifacts[0].Files[0].ID != ref.Files[0].ID {
				t.Fatalf("public reads replaced the accepted artifact or file identity: %+v, %v", accepted, err)
			}
			if terminal != "" {
				if recovered.Status != terminal || !recovered.Progress.Settled || recovered.ReportID != finalized.ReportID ||
					!reflect.DeepEqual(recovered.Output, finalized.Output) || len(recovered.Tasks) != 1 {
					t.Fatalf("late evidence reopened or changed the finalized operation: %+v", recovered)
				}
				if terminal == "completed" {
					after := decodeResponse[models.ReportResponse](t, secretRequest(t, server, http.MethodGet, path+"/report", ""))
					if !reflect.DeepEqual(after, report) {
						t.Fatalf("late evidence rewrote the final report: before=%+v after=%+v", report, after)
					}
				}
			} else {
				decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, path+"/cancel", ""))
			}
			if count := work.Invocations(); count != 1 {
				t.Fatalf("artifact execution was repeated %d times", count)
			}
		})
	}
}
