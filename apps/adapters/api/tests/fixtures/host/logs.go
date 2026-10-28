package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscontrollers "github.com/defos-ai/commander/providers/fs/controllers"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
)

func LogAPIConfig(t *testing.T) api.Config {
	t.Helper()
	root := t.TempDir()
	config := api.Config{
		Security:       api.SecurityConfig{Username: "log-user", Password: "log-password", AllowedHosts: []string{"localhost"}},
		SwaggerEnabled: true, Operations: fakes.NewFailedStart(errors.New("controller is unavailable")),
	}
	var err error
	if config.AgentSourceStore, err = fsagents.NewAgentSourceFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.OperationStore, err = fsoperations.NewOperationFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.MissionStore, err = fsmissions.NewMissionFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.AgentStore, err = fsagents.NewAgentFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ControllerStore, err = fscontrollers.NewControllerFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ActionStore, err = fsactions.NewActionFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ReportStore, err = fsreports.NewReportFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(root); err != nil {
		t.Fatal(err)
	}
	if config.ArtifactFileStore, err = fsartifacts.NewArtifactFileFsStore(root); err != nil {
		t.Fatal(err)
	}
	return config
}

func LogHandler(t *testing.T, config api.Config) http.Handler {
	t.Helper()
	handler, err := api.NewHandler(config)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func LogRequest(ctx context.Context, handler http.Handler, path string, authenticate bool) Response {
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if authenticate {
		request.SetBasicAuth("log-user", "log-password")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}

func LogOperation() operations.Operation {
	created := time.Date(2026, 10, 28, 12, 0, 0, 123456789, time.UTC)
	op := operations.Operation{
		ID: id.New(), Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{Name: "observe", Objectives: []string{"Observe saved execution progress"},
			RequiredActions: []string{"inspect"}, Steps: []missions.Step{{ID: "review"}},
			InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
		Input: json.RawMessage(`{}`),
	}
	for range 2 {
		op.Tasks = append(op.Tasks, operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "inspect"}))
	}
	return op
}

func OperationLogs(operation operations.Operation) []executions.Log {
	logs := make([]executions.Log, 4)
	for index := range logs {
		logs[index] = executions.Log{
			ID: id.New(), AgentID: "worker-agent-00000001", CommandID: id.New(), ActionName: "inspect",
			Work:    messages.WorkContext{OperationID: operation.ID, TaskID: operation.Tasks[0].ID, StepID: "review"},
			Message: "  Compiling <module>\nготово 👋\t", CreatedAt: operation.CreatedAt.Add(-time.Duration(index) * time.Hour),
		}
	}
	logs[1].Work.TaskID, logs[1].AgentID = operation.Tasks[1].ID, "worker-agent-00000002"
	logs[2].ActionName = "Inspect"
	logs[3].Work.StepID = ""
	return logs
}
