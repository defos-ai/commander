package fixtures

import (
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	executionfixtures "github.com/defos-ai/commander/framework/tests/fixtures/executions"
)

type OperationLogData struct {
	Operation operations.Operation
	Task      operations.Task
	Log       executions.Log
	Criteria  executions.LogQuery
}

func OperationLogs() OperationLogData {
	log := executionfixtures.Log()
	log.CommandID = log.Work.TaskID
	task := operations.Task{
		ID: log.Work.TaskID, OperationID: log.Work.OperationID, CommandID: log.CommandID,
		ActionName: log.ActionName, AgentID: log.AgentID,
		Status: operations.TaskRunning, StartedAt: log.CreatedAt,
	}
	return OperationLogData{
		Operation: operations.Operation{ID: log.Work.OperationID, Status: operations.Running, Tasks: []operations.Task{task}},
		Task:      task, Log: log,
		Criteria: executions.LogQuery{
			OperationID: log.Work.OperationID, TaskID: log.Work.TaskID,
			AgentID: log.AgentID, ActionName: log.ActionName,
			AfterLogID: "55555555-5555-4555-8555-555555555555", Limit: 3,
		},
	}
}
