package operations

import (
	"net/http"

	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
)

func Register(mux *http.ServeMux, store operations.OperationStore, reportStore reports.ReportStore, commands controllercommands.Operations) {
	registerOperations(mux, store)
	registerReports(mux, store, reportStore)
	registerTasks(mux, store)
	registerEvents(mux, store)
	registerQuestions(mux, store, commands)
	registerContinuations(mux, commands)
	registerCommands(mux, commands)
}
