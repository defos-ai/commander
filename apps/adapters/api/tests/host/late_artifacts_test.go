package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/controller"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
)

func TestAPILateExecutionArtifactRemainsDownloadable(t *testing.T) {
	for _, terminal := range []string{"", "completed", "cancelled"} {
		name := terminal
		if name == "" {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			server, worker, work, executionStore := startLateArtifactAPI(t, terminal != "")
			operation := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost,
				"/api/v1/operations", `{"mission_name":"late-artifact","input":{}}`), http.StatusCreated)
			path := "/api/v1/operations/" + operation.ID
			queued := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodPost,
				path+"/tasks", `{"action_name":"late-artifact","input":{}}`), http.StatusCreated)
			select {
			case <-work.started:
			case <-time.After(5 * time.Second):
				t.Fatal("artifact action did not start before its deadline")
			}
			var interrupted models.TaskResponse
			awaitSecretCondition(t, "public task interruption", func() bool {
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
			work.unblock()
			var recovered models.OperationResponse
			awaitSecretCondition(t, "late result and artifact through public API", func() bool {
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
			download := server.request(t, http.MethodGet, "/api/v1/artifacts/"+ref.ID+"/files/"+ref.Files[0].ID, authenticate)
			digest := sha256.Sum256([]byte(lateArtifactContents))
			if download.status != http.StatusOK || download.body != lateArtifactContents ||
				ref.Files[0].Size != int64(len(lateArtifactContents)) || ref.Files[0].SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("late artifact bytes do not match their saved descriptor: status=%d file=%+v", download.status, ref.Files[0])
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
			if count := work.invocations.Load(); count != 1 {
				t.Fatalf("artifact execution was repeated %d times", count)
			}
		})
	}
}

const lateArtifactContents = "source archive from the original execution\n"

type lateArtifactAction struct {
	*action.Base
	started     chan struct{}
	release     chan struct{}
	once        sync.Once
	invocations atomic.Int32
	propose     bool
}

func (a *lateArtifactAction) unblock() { a.once.Do(func() { close(a.release) }) }

func (a *lateArtifactAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	invocation := a.invocations.Add(1)
	select {
	case a.started <- struct{}{}:
	default:
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return action.Result{}, ctx.Err()
	}
	result := action.Result{Output: json.RawMessage(`{"summary":"Source ready"}`), Artifacts: []action.Artifact{{
		Name: "Agent source", Files: []artifacts.FileSource{{Name: "source.tar.gz", MediaType: "application/gzip", Data: []byte(lateArtifactContents)}},
	}}}
	if a.propose && invocation == 1 {
		result.NextActions = []action.NextAction{{ActionName: "late-artifact", Input: json.RawMessage(`{}`)}}
	}
	return result, nil
}

func startLateArtifactAPI(t *testing.T, propose bool) (*runningAPI, *agent.Agent, *lateArtifactAction, executions.ExecutionStore) {
	t.Helper()
	fixture := newFixture(t)
	database := filepath.Join(fixture.root, "database")
	config := controller.ControllerConfig{}
	var err error
	if config.AgentStore, err = fsagents.NewAgentFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ActionStore, err = fsactions.NewActionFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(database); err != nil {
		t.Fatal(err)
	}
	bus := newSecretTestBus(t, fixture.root)
	coordinator, err := controller.NewController(t.Context(), bus, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSecretController(t, coordinator) })
	startOperationRunnerWithTimeout(t, coordinator, bus, database, time.Second, missions.Mission{
		Name: "late-artifact", Objectives: []string{"Retain the original execution evidence"},
		RequiredActions: []string{"late-artifact"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	})
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	work := &lateArtifactAction{Base: action.NewBase(actionmodel.Action{Name: "late-artifact",
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}),
		started: make(chan struct{}, 1), release: make(chan struct{}), propose: propose}
	heartbeat := 100 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Type: "artifact-agent", Actions: []action.Executor{work},
		ArtifactFileStore: files, HeartbeatInterval: &heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	agentBus := newSecretTestBus(t, fixture.root)
	go func() { done <- worker.Serve(ctx, agentBus, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("artifact agent shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("artifact agent shutdown timed out")
		}
	})
	t.Cleanup(work.unblock)
	server := startAPI(t, fixture.localArgs())
	awaitSecretCondition(t, "artifact action registration", func() bool {
		registered, err := config.AgentStore.Get(t.Context(), worker.ID())
		return err == nil && registered.Status == "ready"
	})
	return server, worker, work, config.ExecutionStore
}
