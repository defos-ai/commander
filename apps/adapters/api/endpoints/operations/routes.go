package operations

import (
	"net/http"

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/queries"
)

func Register(mux *http.ServeMux, store operations.OperationStore, reportStore reports.ReportStore, agentStore agents.AgentStore, executionStore executions.ExecutionStore, commands controllercommands.Operations) {
	query := queries.Operations{Store: store, Agents: agentStore, Reports: reportStore, Executions: executionStore}
	registerOperations(mux, query)
	registerGraph(mux, store)
	registerReports(mux, query)
	registerTasks(mux, query)
	registerEvents(mux, query)
	registerLogs(mux, query)
	registerQuestions(mux, store, commands)
	registerContinuations(mux, commands)
	registerCommands(mux, commands)
}
