package operations

import (
	"net/http"

	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func Register(mux *http.ServeMux, store operations.OperationStore, reportStore reports.ReportStore, agentStore agents.AgentStore, commands controllercommands.Operations) {
	registerOperations(mux, store, agentStore)
	registerGraph(mux, store)
	registerReports(mux, store, reportStore)
	registerTasks(mux, store, agentStore)
	registerEvents(mux, store)
	registerQuestions(mux, store, commands)
	registerContinuations(mux, commands)
	registerCommands(mux, commands)
}
