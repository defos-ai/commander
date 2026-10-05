package operations

import (
	"net/http"

	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/operations"
)

func Register(mux *http.ServeMux, store operations.OperationStore, commands controllercommands.Operations) {
	registerOperations(mux, store)
	registerTasks(mux, store)
	registerEvents(mux, store)
	registerQuestions(mux, store)
	registerCommands(mux, commands)
}
