package queries_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/queries"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/queries"
)

func TestOperationLogQueriesVerifyScopeAndPreserveCriteria(t *testing.T) {
	for _, taskScoped := range []bool{false, true} {
		name := "operation"
		if taskScoped {
			name = "task"
		}
		t.Run(name, func(t *testing.T) {
			data := fixtures.OperationLogs()
			if !taskScoped {
				data.Criteria.TaskID = ""
			}
			scopes := fakes.NewLogScopes(fakes.LogScopeConfig{Operation: data.Operation, Task: data.Task})
			logs := fakes.NewLogExecutions(fakes.LogExecutionConfig{Items: []executions.Log{data.Log}})
			q := queries.Operations{Store: scopes, Executions: logs, Agents: fakes.AgentStore{}}

			items, err := q.ListLogs(t.Context(), data.Criteria)

			if err != nil || !reflect.DeepEqual(items, []executions.Log{data.Log}) {
				t.Fatalf("scoped log query changed its results: %+v, %v", items, err)
			}
			wantReads := []fakes.LogScopeRead{{Context: t.Context(), OperationID: data.Operation.ID}}
			if taskScoped {
				wantReads = append(wantReads, fakes.LogScopeRead{Context: t.Context(), OperationID: data.Operation.ID, TaskID: data.Task.ID})
			}
			if !reflect.DeepEqual(scopes.Reads(), wantReads) || !reflect.DeepEqual(logs.Reads(), []fakes.LogListRead{{Context: t.Context(), Criteria: data.Criteria}}) {
				t.Fatalf("scope checks or delegated criteria changed: scopes=%+v logs=%+v", scopes.Reads(), logs.Reads())
			}
		})
	}
}

func TestOperationLogQueriesValidateBeforeReadingStores(t *testing.T) {
	for _, invalid := range []string{"missing operation", "operation", "task", "agent", "action", "cursor", "limit"} {
		t.Run(invalid, func(t *testing.T) {
			data := fixtures.OperationLogs()
			switch invalid {
			case "missing operation":
				data.Criteria.OperationID = ""
			case "operation":
				data.Criteria.OperationID = "../operation"
			case "task":
				data.Criteria.TaskID = "task"
			case "agent":
				data.Criteria.AgentID = "builder"
			case "action":
				data.Criteria.ActionName = " "
			case "cursor":
				data.Criteria.AfterLogID = "../log"
			case "limit":
				data.Criteria.Limit = 1001
			}
			scopes := fakes.NewLogScopes(fakes.LogScopeConfig{Operation: data.Operation, Task: data.Task})
			logs := fakes.NewLogExecutions(fakes.LogExecutionConfig{})
			q := queries.Operations{Store: scopes, Executions: logs}

			_, err := q.ListLogs(t.Context(), data.Criteria)

			if !errors.Is(err, domain.ErrInvalidInput) || len(scopes.Reads()) != 0 || len(logs.Reads()) != 0 {
				t.Fatalf("invalid criteria reached a store: error=%v scopes=%+v logs=%+v", err, scopes.Reads(), logs.Reads())
			}
		})
	}
}

func TestOperationLogQueriesPreserveScopeAndStorageFailures(t *testing.T) {
	unavailable := errors.New("saved state unavailable")
	for _, failure := range []string{"unknown operation", "operation storage", "foreign operation", "unknown task", "task storage", "foreign task", "different task", "log storage", "foreign cursor"} {
		t.Run(failure, func(t *testing.T) {
			data := fixtures.OperationLogs()
			scopeConfig := fakes.LogScopeConfig{Operation: data.Operation, Task: data.Task}
			logConfig := fakes.LogExecutionConfig{}
			want, scopeReads, logReads := unavailable, 1, 0
			switch failure {
			case "unknown operation":
				scopeConfig.OperationError, want = operations.ErrUnknownOperation, operations.ErrUnknownOperation
			case "operation storage":
				scopeConfig.OperationError = unavailable
			case "foreign operation":
				scopeConfig.Operation.ID, want = id.OperationID(), operations.ErrUnknownOperation
			case "unknown task":
				scopeConfig.TaskError, want, scopeReads = operations.ErrUnknownTask, operations.ErrUnknownTask, 2
			case "task storage":
				scopeConfig.TaskError, scopeReads = unavailable, 2
			case "foreign task":
				scopeConfig.Task.OperationID, want, scopeReads = id.OperationID(), operations.ErrUnknownTask, 2
			case "different task":
				scopeConfig.Task.ID, want, scopeReads = id.TaskID(), operations.ErrUnknownTask, 2
			case "log storage":
				logConfig.Error, scopeReads, logReads = unavailable, 2, 1
			case "foreign cursor":
				logConfig.Error, want, scopeReads, logReads = executions.ErrUnknownLog, executions.ErrUnknownLog, 2, 1
			}
			scopes := fakes.NewLogScopes(scopeConfig)
			logs := fakes.NewLogExecutions(logConfig)
			q := queries.Operations{Store: scopes, Executions: logs}

			_, err := q.ListLogs(t.Context(), data.Criteria)

			if !errors.Is(err, want) || len(scopes.Reads()) != scopeReads || len(logs.Reads()) != logReads {
				t.Fatalf("failure crossed a scope boundary or lost its identity: error=%v scopes=%+v logs=%+v", err, scopes.Reads(), logs.Reads())
			}
		})
	}
}

func TestOperationLogQueriesHonorCancellationBetweenReads(t *testing.T) {
	for _, stage := range []string{"before operation", "after operation", "after task", "after logs"} {
		t.Run(stage, func(t *testing.T) {
			data := fixtures.OperationLogs()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scopeConfig := fakes.LogScopeConfig{Operation: data.Operation, Task: data.Task}
			logConfig := fakes.LogExecutionConfig{Items: []executions.Log{data.Log}}
			scopeReads, logReads := 0, 0
			switch stage {
			case "before operation":
				cancel()
			case "after operation":
				scopeConfig.CancelAfterOperation, scopeReads = cancel, 1
			case "after task":
				scopeConfig.CancelAfterTask, scopeReads = cancel, 2
			case "after logs":
				logConfig.CancelAfterList, scopeReads, logReads = cancel, 2, 1
			}
			scopes := fakes.NewLogScopes(scopeConfig)
			logs := fakes.NewLogExecutions(logConfig)
			q := queries.Operations{Store: scopes, Executions: logs}

			_, err := q.ListLogs(ctx, data.Criteria)

			if !errors.Is(err, context.Canceled) || len(scopes.Reads()) != scopeReads || len(logs.Reads()) != logReads {
				t.Fatalf("canceled query continued or swallowed cancellation: error=%v scopes=%+v logs=%+v", err, scopes.Reads(), logs.Reads())
			}
		})
	}
}

func TestOperationLogQueriesNormalizeAnEmptyStoreResult(t *testing.T) {
	data := fixtures.OperationLogs()
	scopes := fakes.NewLogScopes(fakes.LogScopeConfig{Operation: data.Operation, Task: data.Task})
	logs := fakes.NewLogExecutions(fakes.LogExecutionConfig{})
	q := queries.Operations{Store: scopes, Executions: logs}

	items, err := q.ListLogs(t.Context(), data.Criteria)

	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty scoped logs must return a nonnil slice: %+v, %v", items, err)
	}
}
