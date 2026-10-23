package operations

import (
	"net/http"

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/queries"
)

func Register(mux *http.ServeMux, store operations.OperationStore, reportStore reports.ReportStore, agentStore agents.AgentStore, commands controllercommands.Operations) {
	query := queries.Operations{Store: store, Agents: agentStore, Reports: reportStore}
	registerOperations(mux, query)
	registerGraph(mux, store)
	registerReports(mux, query)
	registerTasks(mux, query)
	registerEvents(mux, query)
	registerQuestions(mux, store, commands)
	registerContinuations(mux, commands)
	registerCommands(mux, commands)
}
